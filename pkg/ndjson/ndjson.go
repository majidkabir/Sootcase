// Package ndjson reads shipments as newline-delimited JSON (one shipment
// object per line) and writes one result object per line, for piping
// Sootcase between tools such as Kafka consumers and producers.
package ndjson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
)

// DefaultMaxLine is the default longest line accepted, in bytes.
const DefaultMaxLine = 1 << 20

// Reader yields one shipment per non-blank input line. A line that isn't a
// valid shipment becomes an Item with Err set, so one bad message doesn't
// stop the stream. Unknown fields are rejected, so a misspelt field (such
// as "load_factr") can't silently change a result.
type Reader struct {
	br      *bufio.Reader
	line    int
	maxLine int
}

// NewReader returns a Reader accepting lines up to maxLine bytes
// (0 means DefaultMaxLine).
func NewReader(r io.Reader, maxLine int) *Reader {
	if maxLine <= 0 {
		maxLine = DefaultMaxLine
	}
	return &Reader{br: bufio.NewReaderSize(r, 64<<10), maxLine: maxLine}
}

// Next implements batch.Source.
func (r *Reader) Next() (csvio.Item, error) {
	for {
		raw, tooLong, err := r.readLine()
		if len(raw) == 0 && !tooLong && errors.Is(err, io.EOF) {
			return csvio.Item{}, io.EOF
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return csvio.Item{}, fmt.Errorf("line %d: %w", r.line, err)
		}
		if tooLong {
			return csvio.Item{Line: r.line, Err: fmt.Errorf("line %d: longer than %d bytes", r.line, r.maxLine)}, nil
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		it := csvio.Item{Line: r.line}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&it.Shipment); err != nil {
			it.Shipment = calc.Shipment{ID: peekID(raw)}
			it.Err = fmt.Errorf("line %d: invalid shipment JSON: %w", r.line, err)
		} else if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			it.Err = fmt.Errorf("line %d: more than one JSON value on the line", r.line)
		}
		return it, nil
	}
}

// readLine returns the next line without its newline. Lines longer than
// maxLine are consumed and reported with tooLong.
func (r *Reader) readLine() (line []byte, tooLong bool, err error) {
	r.line++
	var buf []byte
	for {
		chunk, err := r.br.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > r.maxLine+1 { // +1 for the newline
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), tooLong, err
	}
}

// peekID best-effort extracts a top-level "id" from an otherwise invalid
// shipment (even a truncated one), so the error can be matched to its message.
func peekID(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return ""
		}
		if k == "id" {
			if v, err := dec.Token(); err == nil {
				if s, ok := v.(string); ok {
					return s
				}
			}
			return ""
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return ""
		}
	}
	return ""
}

// Out is one output line.
type Out struct {
	Line       int          `json:"line"`
	ShipmentID string       `json:"shipment_id,omitempty"`
	Result     *calc.Result `json:"result,omitempty"`
	Error      string       `json:"error,omitempty"`
}

// Writer writes one JSON object per line.
type Writer struct{ enc *json.Encoder }

// NewWriter returns a Writer.
func NewWriter(w io.Writer) *Writer { return &Writer{enc: json.NewEncoder(w)} }

// Write writes the result for one item, or its error.
func (w *Writer) Write(it csvio.Item, res calc.Result, err error) error {
	o := Out{Line: it.Line, ShipmentID: it.Shipment.ID}
	if err != nil {
		o.Error = err.Error()
	} else {
		o.Result = &res
	}
	return w.enc.Encode(o)
}
