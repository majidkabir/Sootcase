package api

import (
	"context"
	"errors"
	"mime"
	"net/http"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
)

// isCSV says whether the request body is a shipments CSV (text/csv); the
// other inputs are JSON.
func isCSV(r *http.Request) bool {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return ct == "text/csv"
}

// readCSV reads a shipments CSV body of at most maxBodyBytes. When the file
// can't be read it writes the error response and returns ok false.
func readCSV(w http.ResponseWriter, r *http.Request) (items []csvio.Item, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	items, err := csvio.Read(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return nil, false
	}
	return items, true
}

// split separates the shipments read from the CSV rows that failed.
func split(items []csvio.Item) (shipments []calc.Shipment, failed []csvio.Item) {
	for _, it := range items {
		if it.Err != nil {
			failed = append(failed, it)
		} else {
			shipments = append(shipments, it.Shipment)
		}
	}
	return shipments, failed
}

// errNoShipments refuses a report, scenario or recommendation without any
// shipment.
var errNoShipments = errors.New("no shipments")

// errNoTurn is the answer when a big job waited too long for its turn.
var errNoTurn = errors.New("other big files are being calculated and this one didn't get its turn in time; try again in a few minutes")

// bigTurn waits for a turn when n shipments are a big job (n >= big): big
// jobs run Limits.BigConcurrent at a time. Call release when done. When the
// request ends first it writes the error response and returns ok false.
func (s *Server) bigTurn(ctx context.Context, w http.ResponseWriter, n, big int) (release func(), ok bool) {
	if n < big {
		return func() {}, true
	}
	select {
	case s.big <- struct{}{}:
		return func() { <-s.big }, true
	case <-ctx.Done():
		writeError(w, http.StatusServiceUnavailable, errNoTurn)
		return nil, false
	}
}
