package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/scenario"
)

// maxScenarioItems bounds shipments per scenario request: each is
// calculated up to four times.
const maxScenarioItems = 2_500

// ScenarioRequest is the JSON body of POST /v1/scenario.
type ScenarioRequest struct {
	Plan      scenario.Plan   `json:"plan"`
	Shipments []calc.Shipment `json:"shipments"`
}

// scenarioRun compares shipments with a plan applied: a JSON body
// (ScenarioRequest), or a shipments CSV (text/csv) with the plan as JSON
// in the plan query parameter.
func (s *Server) scenarioRun(w http.ResponseWriter, r *http.Request) {
	var req ScenarioRequest
	var parseErrs []csvio.Item
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "text/csv" {
		if err := json.Unmarshal([]byte(r.URL.Query().Get("plan")), &req.Plan); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("plan query parameter: invalid JSON: %w", err))
			return
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
	if n := len(req.Shipments) + len(parseErrs); n > maxScenarioItems {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("%d shipments, max is %d for a scenario", n, maxScenarioItems))
		return
	}
	if _, err := req.Plan.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	if len(req.Shipments) >= s.Limits.BigBatch/4 {
		select {
		case s.big <- struct{}{}:
			defer func() { <-s.big }()
		case <-ctx.Done():
			writeError(w, http.StatusServiceUnavailable, errors.New("other big files are being calculated and this one didn't get its turn in time; try again in a few minutes"))
			return
		}
	}
	var valid []calc.Shipment
	var sizeErrs []scenario.ShipmentResult
	for _, sh := range req.Shipments {
		if err := s.checkSize(sh); err != nil {
			sizeErrs = append(sizeErrs, scenario.ShipmentResult{ShipmentID: sh.ID, Error: err.Error()})
			continue
		}
		valid = append(valid, sh)
	}
	s.Calc.Prefetch(ctx, valid)
	res, err := scenario.RunParallel(ctx, s.Calc, valid, req.Plan, s.Limits.BatchWorkers)
	if err != nil {
		if errors.Is(err, scenario.ErrInvalidPlan) {
			writeError(w, http.StatusBadRequest, err)
		} else {
			writeError(w, calcStatus(err), err)
		}
		return
	}
	for _, it := range parseErrs {
		res.Shipments = append(res.Shipments, scenario.ShipmentResult{ShipmentID: it.Shipment.ID, Error: it.Err.Error()})
		res.Failed++
	}
	res.Shipments = append(res.Shipments, sizeErrs...)
	res.Failed += len(sizeErrs)
	writeJSON(w, http.StatusOK, res)
}
