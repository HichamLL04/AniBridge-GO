package middlewares

import (
	"net/http"

	 "anibridge-go/internal/config"
	 "anibridge-go/internal/utils"
)

func BasicAuth(cfg config.WebConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if cfg.Username == "" && cfg.Password == "" && cfg.Htpasswd == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || !utils.CheckBasic(user, pass, cfg.Username, cfg.Password, cfg.Htpasswd) {
				w.Header().Set("WWW-Authenticate", `Basic realm="AniBridge GO"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
