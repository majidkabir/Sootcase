// Package api exposes the calculator over HTTP.
package api

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/explain"
	"github.com/majidkabir/sootcase/pkg/places"
	"github.com/majidkabir/sootcase/pkg/report"
)

//go:embed openapi.yaml
var openAPISpec []byte

//go:embed web/index.html
var indexHTML []byte

// Leaflet 1.9.4 (BSD-2-Clause, web/leaflet/LICENSE) draws routes on a map.
//
//go:embed web/leaflet
var leafletFS embed.FS

const (
	maxBodyBytes  = 10 << 20 // 10 MiB
	maxBatchItems = 10_000
)

// Server holds the handler dependencies.
type Server struct {
	Calc    calc.Calculator
	Factors any // served as-is on GET /v1/factors
	// Places backs GET /v1/places (default: the embedded place list).
	Places func() (*places.Index, error)
	Log    *slog.Logger
	Limits Limits

	sem chan struct{}
	big chan struct{} // turns for big batches (Limits.BigBatch)
}

// Limits protect the server from overload. Zero values use the defaults.
type Limits struct {
	// BatchWorkers is how many shipments of one batch or report request
	// are calculated at once (default 4 x CPUs; routed legs wait on the
	// router, so more workers than CPUs pays off).
	BatchWorkers int
	// MaxConcurrent is how many calculation requests run at once; more get
	// 503 with Retry-After. Default 4 x GOMAXPROCS.
	MaxConcurrent int
	// Timeout bounds each calculation request; past it the request gets
	// 504. Default 30s.
	Timeout time.Duration
	// MaxLegs bounds legs (and hubs) per shipment. Default 100.
	MaxLegs int
	// Batches and reports of BigBatch shipments or more (default 1,000)
	// take turns: at most BigConcurrent (default 2) run at once, the rest
	// wait in line, first come first served, within their Timeout. Running
	// them all at once would only make every one of them slow.
	BigBatch      int
	BigConcurrent int
}

func (l Limits) withDefaults() Limits {
	if l.BatchWorkers <= 0 {
		l.BatchWorkers = 4 * runtime.GOMAXPROCS(0)
	}
	if l.MaxConcurrent <= 0 {
		l.MaxConcurrent = 4 * runtime.GOMAXPROCS(0)
	}
	if l.Timeout <= 0 {
		l.Timeout = 30 * time.Second
	}
	if l.MaxLegs <= 0 {
		l.MaxLegs = 100
	}
	if l.BigBatch <= 0 {
		l.BigBatch = 1000
	}
	if l.BigConcurrent <= 0 {
		l.BigConcurrent = 2
	}
	return l
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	s.Limits = s.Limits.withDefaults()
	s.sem = make(chan struct{}, s.Limits.MaxConcurrent)
	s.big = make(chan struct{}, s.Limits.BigConcurrent)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; "+
				"img-src 'self' data: https://tile.openstreetmap.org")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /static/{file}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		b, err := leafletFS.ReadFile("web/leaflet/" + name)
		if err != nil || (name != "leaflet.js" && name != "leaflet.css") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		w.Header().Set("Cache-Control", "public, max-age=604800")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec)
	})
	mux.HandleFunc("GET /v1/factors", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.Factors)
	})
	mux.HandleFunc("GET /v1/places", s.places)
	mux.HandleFunc("POST /v1/calculate", s.limited(s.calculate))
	mux.HandleFunc("POST /v1/calculate/batch", s.limited(s.calculateBatch))
	mux.HandleFunc("POST /v1/report", s.limited(s.report))
	mux.HandleFunc("POST /v1/scenario", s.limited(s.scenarioRun))
	mux.HandleFunc("POST /v1/recommend", s.limited(s.recommendRun))
	var h http.Handler = s.recoverPanics(secureHeaders(mux))
	if s.Log != nil {
		h = s.logRequests(h)
	}
	return h
}

// limited applies the concurrency cap and request timeout.
func (s *Server) limited(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, errors.New("server busy, retry shortly"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.Limits.Timeout)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		// ?add_road_legs=false: no road legs added for shipments that don't
		// say (see calc.Shipment.AddRoadLegs).
		switch r.URL.Query().Get("add_road_legs") {
		case "", "true", "1":
		case "false", "0":
			ctx = calc.WithoutRoadLegs(ctx)
		default:
			writeError(w, http.StatusBadRequest, errors.New("add_road_legs must be true or false"))
			return
		}
		h(w, r.WithContext(ctx))
	}
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				if s.Log != nil {
					s.Log.Error("panic", "path", r.URL.Path, "error", fmt.Sprint(v), "stack", string(debug.Stack()))
				}
				writeError(w, http.StatusInternalServerError, errors.New("internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// checkSize rejects shipments too large to calculate in one request.
func (s *Server) checkSize(sh calc.Shipment) error {
	if len(sh.Legs) > s.Limits.MaxLegs || len(sh.Hubs) > s.Limits.MaxLegs {
		return fmt.Errorf("shipment has %d legs and %d hubs; max is %d each", len(sh.Legs), len(sh.Hubs), s.Limits.MaxLegs)
	}
	return nil
}

// calcStatus maps a calculation error to an HTTP status.
func calcStatus(err error) int {
	switch {
	case calc.Temporary(err):
		return http.StatusServiceUnavailable // not the input's fault: try again
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return 499 // client closed request; nobody reads it
	default:
		return http.StatusUnprocessableEntity
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.Log.Info("request", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration_ms", float64(time.Since(start).Microseconds())/1000)
	})
}

func (s *Server) calculate(w http.ResponseWriter, r *http.Request) {
	var sh calc.Shipment
	if !s.decode(w, r, &sh) {
		return
	}
	if err := s.checkSize(sh); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, err)
		return
	}
	ctx := r.Context()
	if v := r.URL.Query().Get("path"); v == "true" || v == "1" {
		ctx = calc.WithPath(ctx) // each leg's route for a map; single calculations only
	}
	res, err := s.Calc.CalculateRetrying(ctx, sh)
	if err != nil {
		writeError(w, calcStatus(err), err)
		return
	}
	if v := r.URL.Query().Get("explain"); v == "true" || v == "1" {
		sheet := explain.Shipment(sh, res, s.Calc.Factors)
		writeJSON(w, http.StatusOK, ExplainedResult{Result: res, Explanation: &sheet})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ExplainedResult is the body of POST /v1/calculate?explain=true: the
// result, and how it was calculated step by step with the GLEC Framework
// and ISO 14083 basis of each step.
type ExplainedResult struct {
	calc.Result
	Explanation *explain.Sheet `json:"explanation,omitempty"`
}

// PlacesResponse is the body returned by GET /v1/places.
type PlacesResponse struct {
	Places []places.Place `json:"places"`
}

// places searches cities, ports, terminals and airports by name or code:
// ?q=rotterdam&kind=port,rail&limit=10
func (s *Server) places(w http.ResponseWriter, r *http.Request) {
	load := s.Places
	if load == nil {
		load = places.Default
	}
	ix, err := load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	q := r.URL.Query()
	if len(q.Get("q")) > 100 {
		writeError(w, http.StatusBadRequest, errors.New("q is too long"))
		return
	}
	var kinds []string
	for _, k := range strings.Split(q.Get("kind"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			kinds = append(kinds, k)
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	limit = max(1, min(limit, 50))
	if q.Get("limit") == "" {
		limit = 10
	}
	res := ix.Search(q.Get("q"), kinds, limit)
	if res == nil {
		res = []places.Place{}
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	writeJSON(w, http.StatusOK, PlacesResponse{Places: res})
}

// BatchRequest is the body of POST /v1/calculate/batch.
type BatchRequest struct {
	Shipments []calc.Shipment `json:"shipments"`
}

// BatchItem is one entry in a batch response: a result or an error.
type BatchItem struct {
	ShipmentID string       `json:"shipment_id"`
	Result     *calc.Result `json:"result,omitempty"`
	Error      string       `json:"error,omitempty"`
	// Retryable marks a failure that isn't the input's fault (a routing
	// service didn't answer, after retries): sending it again can work.
	Retryable bool `json:"retryable,omitempty"`
}

// BatchResponse is the body returned by POST /v1/calculate/batch.
type BatchResponse struct {
	Results []BatchItem `json:"results"`
	Failed  int         `json:"failed"`
	// Retryable counts the failures worth sending again (BatchItem.Retryable).
	Retryable int `json:"retryable,omitempty"`
}

func (s *Server) calculateBatch(w http.ResponseWriter, r *http.Request) {
	var req BatchRequest
	if !s.decode(w, r, &req) {
		return
	}
	resp := BatchResponse{Results: make([]BatchItem, 0, len(req.Shipments))}
	ok := s.each(w, r, req.Shipments, func(id string, res calc.Result, err error) {
		item := BatchItem{ShipmentID: id}
		if err != nil {
			item.Error, item.Retryable = err.Error(), calc.Temporary(err)
			resp.Failed++
			if item.Retryable {
				resp.Retryable++
			}
		} else {
			item.Result = &res
		}
		resp.Results = append(resp.Results, item)
	})
	if ok {
		writeJSON(w, http.StatusOK, resp)
	}
}

// each calculates shipments, passing each result or error to fn in input
// order. Distances for the batch's lanes are fetched first in bulk where
// the distance provider can, then shipments run on parallel workers. It
// writes an error response and returns false if the batch is too big or
// the request times out or is cancelled; a shipment that merely fails is
// passed to fn.
func (s *Server) each(w http.ResponseWriter, r *http.Request, shipments []calc.Shipment, fn func(string, calc.Result, error)) bool {
	if len(shipments) > maxBatchItems {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Errorf("batch has %d shipments, max is %d", len(shipments), maxBatchItems))
		return false
	}
	ctx := r.Context()
	if len(shipments) >= s.Limits.BigBatch {
		// Wait for a turn: big batches run BigConcurrent at a time.
		select {
		case s.big <- struct{}{}:
			defer func() { <-s.big }()
		case <-ctx.Done():
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("other big files are being calculated and this one didn't get its turn in time; try again in a few minutes"))
			return false
		}
	}
	type out struct {
		res  calc.Result
		err  error
		done bool // calculated, without being cut off by the request's end
	}
	results := make([]out, len(shipments))
	var valid []calc.Shipment
	for i, sh := range shipments {
		if err := s.checkSize(sh); err != nil {
			results[i].err = err
			continue
		}
		valid = append(valid, sh)
	}
	s.Calc.Prefetch(ctx, valid)

	workers := s.Limits.BatchWorkers
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(shipments)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				if results[i].err == nil {
					results[i].res, results[i].err = s.Calc.CalculateRetrying(ctx, shipments[i])
				}
				results[i].done = ctx.Err() == nil
			}
		}()
	}
feed:
	for i := range shipments {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil { // timed out or client left: don't blame shipments
		done := 0
		for _, o := range results {
			if o.done {
				done++
			}
		}
		writeError(w, calcStatus(err), fmt.Errorf("batch stopped after %d of %d shipments: %w", done, len(shipments), err))
		return false
	}
	for i, o := range results {
		fn(shipments[i].ID, o.res, o.err)
	}
	return true
}

// ReportRequest is the body of POST /v1/report.
type ReportRequest struct {
	report.Meta
	Shipments []calc.Shipment `json:"shipments"`
}

// ReportResponse is the body returned by POST /v1/report: the report, and
// with ?markdown=true its Markdown, with ?results=true each shipment's
// full result (as in a batch, in input order).
type ReportResponse struct {
	report.Report
	Markdown string      `json:"markdown,omitempty"`
	Results  []BatchItem `json:"results,omitempty"`
}

// maxMetaLen caps each free-text report field.
const maxMetaLen = 1000

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	var req ReportRequest
	var parseErrs []csvio.Item // CSV shipments that failed to parse
	var csvOrder []string      // CSV shipment ids in file order
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "text/csv" {
		// A shipments CSV as the body, report details in the query.
		q := r.URL.Query()
		req.Meta = report.Meta{Title: q.Get("title"), Organisation: q.Get("organisation"),
			PeriodFrom: q.Get("period_from"), PeriodTo: q.Get("period_to"), Reference: q.Get("reference"),
			DeclareISO: q.Get("declare_iso") == "true"}
		if v := q.Get("scope3_category"); v != "" {
			req.Meta.Scope3Category, _ = strconv.Atoi(v)
			if req.Meta.Scope3Category == 0 {
				req.Meta.Scope3Category = -1 // refused below
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
		if len(items) > maxBatchItems {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Errorf("file has %d shipments, max is %d", len(items), maxBatchItems))
			return
		}
		for _, it := range items {
			csvOrder = append(csvOrder, it.Shipment.ID)
			if it.Err != nil {
				parseErrs = append(parseErrs, it)
			} else {
				req.Shipments = append(req.Shipments, it.Shipment)
			}
		}
	} else if !s.decode(w, r, &req) {
		return
	}
	m := req.Meta
	for _, f := range []string{m.Title, m.Organisation, m.Reference, m.PeriodFrom, m.PeriodTo} {
		if len(f) > maxMetaLen {
			writeError(w, http.StatusBadRequest, fmt.Errorf("report text fields are limited to %d bytes", maxMetaLen))
			return
		}
	}
	if m.Scope3Category != 0 && m.Scope3Category != 4 && m.Scope3Category != 9 {
		writeError(w, http.StatusBadRequest, errors.New("scope3_category must be 4 (transport the company pays for) or 9 (sold products, transport it doesn't pay for)"))
		return
	}
	for _, d := range []string{m.PeriodFrom, m.PeriodTo} {
		if _, err := time.Parse(time.DateOnly, d); d != "" && err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("period dates must be YYYY-MM-DD, got %q", d))
			return
		}
	}
	b := report.New(m)
	q := r.URL.Query()
	withResults := q.Get("results") == "true"
	byID := map[string]BatchItem{}
	add := func(id string, res calc.Result, err error) {
		b.Add(id, res, err)
		if !withResults {
			return
		}
		item := BatchItem{ShipmentID: id}
		if err != nil {
			item.Error, item.Retryable = err.Error(), calc.Temporary(err)
		} else {
			item.Result = &res
		}
		byID[id] = item
	}
	for _, it := range parseErrs {
		add(it.Shipment.ID, calc.Result{}, it.Err)
	}
	if !s.each(w, r, req.Shipments, add) {
		return
	}
	rep := b.Report()
	if q.Get("format") != "md" && (withResults || q.Get("markdown") == "true") {
		resp := ReportResponse{Report: rep}
		if q.Get("markdown") == "true" {
			var buf bytes.Buffer
			if err := report.WriteMarkdown(&buf, rep); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			resp.Markdown = buf.String()
		}
		if withResults {
			// In input order: the CSV's (failed rows included), or the JSON's.
			ids := make([]string, 0, len(byID))
			if len(csvOrder) > 0 {
				ids = csvOrder
			} else {
				for _, sh := range req.Shipments {
					ids = append(ids, sh.ID)
				}
			}
			resp.Results = make([]BatchItem, 0, len(ids))
			for _, id := range ids {
				if it, ok := byID[id]; ok {
					resp.Results = append(resp.Results, it)
					delete(byID, id) // a repeated id once
				}
			}
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if q.Get("format") == "md" {
		var buf bytes.Buffer
		if err := report.WriteMarkdown(&buf, rep); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// decode reads a JSON body into v, writing a 4xx and returning false on failure.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, err)
		} else {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		}
		return false
	}
	return true
}

type errorBody struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, err error) {
	if status == http.StatusServiceUnavailable && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "5")
	}
	writeJSON(w, status, errorBody{Error: err.Error()})
}

// writeJSON encodes before writing, so an encoding failure becomes a 500
// instead of a truncated 200.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		b, _ = json.Marshal(errorBody{Error: "could not encode response: " + err.Error()})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
