package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"

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

// decodeStrict decodes one JSON value from r into v: unknown fields and
// anything after the value are errors, so a typo is never silently ignored.
func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("more data after the JSON value")
		}
		return err
	}
	return nil
}

// boolParam reads a true/false query parameter: true or 1, false or 0,
// empty for false. Anything else is an error, so a typo isn't ignored.
func boolParam(q url.Values, name string) (bool, error) {
	switch q.Get(name) {
	case "true", "1":
		return true, nil
	case "", "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false", name)
}

// boolParams reads several true/false query parameters; on an error it
// writes the response and returns ok false.
func boolParams(w http.ResponseWriter, q url.Values, names ...string) (vals map[string]bool, ok bool) {
	vals = make(map[string]bool, len(names))
	for _, n := range names {
		v, err := boolParam(q, n)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return nil, false
		}
		vals[n] = v
	}
	return vals, true
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
	default:
	}
	// Wait for the turn without holding a concurrency slot, so queued big
	// jobs can't keep small requests out; take a slot again once it's our
	// turn.
	sl, _ := ctx.Value(slotKey{}).(*slot)
	if sl != nil && sl.held {
		<-s.sem
		sl.held = false
	}
	select {
	case s.big <- struct{}{}:
	case <-ctx.Done():
		writeError(w, http.StatusServiceUnavailable, errNoTurn)
		return nil, false
	}
	if sl != nil {
		select {
		case s.sem <- struct{}{}:
			sl.held = true
		case <-ctx.Done():
			<-s.big
			writeError(w, http.StatusServiceUnavailable, errNoTurn)
			return nil, false
		}
	}
	return func() { <-s.big }, true
}

// slot is a request's concurrency slot (Server.limited), which a big job
// gives up while it waits for its turn.
type slot struct{ held bool }

type slotKey struct{}
