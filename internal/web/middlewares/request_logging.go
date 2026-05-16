package middlewares

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func RequestLogging(level string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if strings.ToLower(level) != "debug" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			slog.Debug("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
		})
	}
}
