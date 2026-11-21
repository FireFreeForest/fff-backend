package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(level, format string) *slog.Logger {
	return NewWithWriter(level, format, os.Stdout)
}

// NewWithWriter is New with a custom destination. The stdio MCP transport uses
// stderr so stdout stays reserved for JSON-RPC frames.
func NewWithWriter(level, format string, w io.Writer) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if strings.EqualFold(format, "json") {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	l := slog.New(handler)
	slog.SetDefault(l)
	return l
}
