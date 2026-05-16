package webhook

import (
	"database/sql"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type Deps struct {
	DB *sql.DB
}

func Router(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Post("/{provider}", func(w http.ResponseWriter, r *http.Request) {
		provider := chi.URLParam(r, "provider")
		_, _ = io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		_, _ = d.DB.ExecContext(r.Context(), "INSERT INTO sync_history(profile, provider, item_id, action, status, message, dry_run, created_at) VALUES(?,?,?,?,?,?,?,?)", "webhook", provider, "*", "webhook", "accepted", "webhook queued", false, time.Now())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return r
}
