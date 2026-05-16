package utils

import (
	"context"
	"io"
	"log/slog"
	"strings"

	 "anibridge-go/internal/web/services"
)

const LevelSuccess slog.Level = 2

type sinkHandler struct {
	base slog.Handler
	sink func(services.LogEntry)
}

func NewLogger(level string, out io.Writer) *slog.Logger {
	return slog.New(NewHandler(level, out, nil))
}

func NewLoggerWithSink(level string, out io.Writer, sink func(services.LogEntry)) *slog.Logger {
	return slog.New(NewHandler(level, out, sink))
}

func NewHandler(level string, out io.Writer, sink func(services.LogEntry)) slog.Handler {
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return &sinkHandler{base: slog.NewJSONHandler(out, &slog.HandlerOptions{Level: lvl, ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.LevelKey && a.Value.Any() == LevelSuccess {
			return slog.String(slog.LevelKey, "SUCCESS")
		}
		return a
	}}), sink: sink}
}

func (h *sinkHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}
func (h *sinkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sinkHandler{base: h.base.WithAttrs(attrs), sink: h.sink}
}
func (h *sinkHandler) WithGroup(name string) slog.Handler {
	return &sinkHandler{base: h.base.WithGroup(name), sink: h.sink}
}
func (h *sinkHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.sink != nil {
		attrs := map[string]any{}
		r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.Any(); return true })
		h.sink(services.LogEntry{Timestamp: r.Time.Format("2006-01-02 15:04:05"), Level: r.Level.String(), Message: r.Message, Attrs: attrs})
	}
	return h.base.Handle(ctx, r)
}
