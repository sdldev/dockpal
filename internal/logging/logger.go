package logging

import (
	"io"
	"log/slog"
)

func NewJSONLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{}))
}

func ConfigureJSON(w io.Writer) *slog.Logger {
	logger := NewJSONLogger(w)
	slog.SetDefault(logger)
	return logger
}
