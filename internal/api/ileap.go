package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/ileap"
	"github.com/majidkabir/sootcase/pkg/places"
	"github.com/majidkabir/sootcase/pkg/toc"
)

// ILEAPMeta is what iLEAP asks about the data that a calculation doesn't
// know: the company owning it and the period it covers (first and last
// day, YYYY-MM-DD).
type ILEAPMeta struct {
	Company    string   `json:"company"`
	CompanyIDs []string `json:"company_ids,omitempty"`
	PeriodFrom string   `json:"period_from,omitempty"`
	PeriodTo   string   `json:"period_to,omitempty"`
}

// ILEAPRequest is the JSON body of POST /v1/ileap.
type ILEAPRequest struct {
	ILEAPMeta
	Shipments []calc.Shipment `json:"shipments"`
}

// ILEAPOperationsRequest is the JSON body of POST /v1/ileap/operations.
type ILEAPOperationsRequest struct {
	ILEAPMeta
	Operations toc.Operations `json:"operations"`
}

// ILEAPResponse is an iLEAP export, and the shipments that couldn't be
// calculated (so aren't in it).
type ILEAPResponse struct {
	ileap.Export
	Failed []BatchItem `json:"failed,omitempty"`
}

func (m ILEAPMeta) options(fuels ileap.FuelLookup) (ileap.Options, error) {
	o := ileap.Options{CompanyName: strings.TrimSpace(m.Company), CompanyIDs: m.CompanyIDs, CreatedAt: time.Now(),
		Fuels: fuels, Country: places.Codes{}.Country}
	if len(o.CompanyName) > maxMetaLen || len(o.CompanyIDs) > 20 {
		return o, fmt.Errorf("company is limited to %d bytes and 20 ids", maxMetaLen)
	}
	for _, id := range o.CompanyIDs {
		if strings.TrimSpace(id) == "" || len(id) > maxMetaLen {
			return o, errors.New("company_ids must be non-empty strings")
		}
	}
	if m.PeriodFrom == "" && m.PeriodTo == "" {
		return o, nil
	}
	a, err1 := time.Parse(time.DateOnly, m.PeriodFrom)
	b, err2 := time.Parse(time.DateOnly, m.PeriodTo)
	if err1 != nil || err2 != nil || b.Before(a) {
		return o, errors.New("give period_from and period_to (the first and the last day) as YYYY-MM-DD")
	}
	o.PeriodStart, o.PeriodEnd = a, b.AddDate(0, 0, 1)
	return o, nil
}

func metaFromQuery(r *http.Request) ILEAPMeta {
	q := r.URL.Query()
	m := ILEAPMeta{Company: q.Get("company"), PeriodFrom: q.Get("period_from"), PeriodTo: q.Get("period_to")}
	for _, id := range strings.Split(q.Get("company_ids"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			m.CompanyIDs = append(m.CompanyIDs, id)
		}
	}
	return m
}

// ileapShipments calculates shipments and returns them as iLEAP
// ShipmentFootprints with their TOCs and HOCs: a JSON body
// (ILEAPRequest), or a shipments CSV (text/csv) with company, company_ids,
// period_from and period_to in the query.
func (s *Server) ileapShipments(w http.ResponseWriter, r *http.Request) {
	var req ILEAPRequest
	var parseErrs []csvio.Item
	if isCSV(r) {
		req.ILEAPMeta = metaFromQuery(r)
		items, ok := readCSV(w, r)
		if !ok {
			return
		}
		req.Shipments, parseErrs = split(items)
	} else if !s.decode(w, r, &req) {
		return
	}
	if n := len(req.Shipments) + len(parseErrs); n > maxBatchItems {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("%d shipments, max is %d", n, maxBatchItems))
		return
	} else if n == 0 {
		writeError(w, http.StatusBadRequest, errNoShipments)
		return
	}
	fuels, _ := s.Calc.Factors.(ileap.FuelLookup)
	o, err := req.options(fuels)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var failed []BatchItem
	for _, it := range parseErrs {
		failed = append(failed, BatchItem{ShipmentID: it.Shipment.ID, Error: it.Err.Error()})
	}
	var results []calc.Result
	if !s.each(w, r, req.Shipments, func(id string, res calc.Result, err error) {
		if err != nil {
			failed = append(failed, BatchItem{ShipmentID: id, Error: err.Error(), Retryable: calc.Temporary(err)})
			return
		}
		results = append(results, res)
	}) {
		return
	}
	writeJSON(w, http.StatusOK, ILEAPResponse{Export: ileap.FromResults(results, o), Failed: failed})
}

// ileapOperations returns an operator's measured data (the operations
// of "sootcase toc") as iLEAP TOCs and HOCs.
func (s *Server) ileapOperations(w http.ResponseWriter, r *http.Request) {
	var req ILEAPOperationsRequest
	if !s.decode(w, r, &req) {
		return
	}
	ops, err := toc.Check(req.Operations)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fuels, ok := s.Calc.Factors.(calc.FactorProvider)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("no fuel factors"))
		return
	}
	o, err := req.options(fuels)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, ILEAPResponse{Export: ileap.FromOperations(ops, fuels, o)})
}
