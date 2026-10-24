package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/recommend"
)

// maxRecommendItems bounds shipments per recommendation request: each is
// calculated up to about 15 times (fewer when shipments share their legs).
const maxRecommendItems = 1_000

// RecommendRequest is the JSON body of POST /v1/recommend.
type RecommendRequest struct {
	Settings  recommend.Settings `json:"settings"`
	Shipments []calc.Shipment    `json:"shipments"`
}

// recommendRun finds lower-emission alternatives for shipments: a JSON
// body (RecommendRequest), or a shipments CSV (text/csv) with the settings
// as JSON in the settings query parameter (optional).
func (s *Server) recommendRun(w http.ResponseWriter, r *http.Request) {
	var req RecommendRequest
	var parseErrs []csvio.Item
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "text/csv" {
		if q := r.URL.Query().Get("settings"); q != "" {
			if err := json.Unmarshal([]byte(q), &req.Settings); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("settings query parameter: invalid JSON: %w", err))
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		items, err := csvio.Read(r.Body)
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeError(w, http.StatusRequestEntityTooLarge, err)
			} else {
				writeError(w, http.StatusBadRequest, err)
			}
			return
		}
		for _, it := range items {
			if it.Err != nil {
				parseErrs = append(parseErrs, it)
			} else {
				req.Shipments = append(req.Shipments, it.Shipment)
			}
		}
	} else if !s.decode(w, r, &req) {
		return
	}
	if n := len(req.Shipments) + len(parseErrs); n > maxRecommendItems {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("%d shipments, max is %d for recommendations", n, maxRecommendItems))
		return
	}
	if len(req.Shipments)+len(parseErrs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no shipments"))
		return
	}
	if _, _, err := req.Settings.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	if len(req.Shipments) >= s.Limits.BigBatch/16 {
		select {
		case s.big <- struct{}{}:
			defer func() { <-s.big }()
		case <-ctx.Done():
			writeError(w, http.StatusServiceUnavailable, errors.New("other big files are being calculated and this one didn't get its turn in time; try again in a few minutes"))
			return
		}
	}
	var valid []calc.Shipment
	var sizeErrs []recommend.ShipmentResult
	for _, sh := range req.Shipments {
		if err := s.checkSize(sh); err != nil {
			sizeErrs = append(sizeErrs, recommend.ShipmentResult{ShipmentID: sh.ID, Error: err.Error()})
			continue
		}
		valid = append(valid, sh)
	}
	s.Calc.Prefetch(ctx, valid)
	res, err := recommend.Run(ctx, s.Calc, valid, req.Settings, s.Limits.BatchWorkers)
	if err != nil {
		if errors.Is(err, recommend.ErrInvalidSettings) {
			writeError(w, http.StatusBadRequest, err)
		} else {
			writeError(w, calcStatus(err), err)
		}
		return
	}
	for _, it := range parseErrs {
		res.Shipments = append(res.Shipments, recommend.ShipmentResult{ShipmentID: it.Shipment.ID, Error: it.Err.Error()})
		res.Failed++
	}
	res.Shipments = append(res.Shipments, sizeErrs...)
	res.Failed += len(sizeErrs)
	writeJSON(w, http.StatusOK, res)
}
