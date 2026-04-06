package output

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type contextKey string

const writerContextKey contextKey = "output-writer"

type Writer struct {
	out  io.Writer
	json bool
}

func New(out io.Writer, useJSON bool) *Writer {
	return &Writer{out: out, json: useJSON}
}

func WithContext(ctx context.Context, w *Writer) context.Context {
	return context.WithValue(ctx, writerContextKey, w)
}

func FromContext(ctx context.Context) *Writer {
	if w, ok := ctx.Value(writerContextKey).(*Writer); ok && w != nil {
		return w
	}
	return New(io.Discard, false)
}

func (w *Writer) IsJSON() bool {
	return w != nil && w.json
}

func (w *Writer) Line(format string, args ...any) {
	if w == nil || w.out == nil {
		return
	}
	_, _ = fmt.Fprintf(w.out, format+"\n", args...)
}

func (w *Writer) JSON(v any) error {
	if w == nil || w.out == nil {
		return nil
	}
	enc := json.NewEncoder(w.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
