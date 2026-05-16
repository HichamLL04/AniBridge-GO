package web

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	 "anibridge-go/internal/config"
	 "anibridge-go/internal/core/sched"
	 "anibridge-go/internal/web/middlewares"
	 "anibridge-go/internal/web/routes/api"
	 "anibridge-go/internal/web/routes/webhook"
	wsroutes  "anibridge-go/internal/web/routes/ws"
	 "anibridge-go/internal/web/routes/z"
	 "anibridge-go/internal/web/services"
)

type Deps struct {
	Config       config.Config
	ConfigPath   string
	DB           *sql.DB
	Hub          *services.Hub
	Logs         *services.LogStore
	Scheduler    *sched.Client
	StartedAt    time.Time
	FrontendRoot string
}

func New(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(middlewares.RequestLogging(deps.Config.LogLevel))
	r.Use(middlewares.BasicAuth(deps.Config.Web))

	r.Mount("/api", api.Router(api.Deps(deps)))
	r.Mount("/webhook", webhook.Router(webhook.Deps{DB: deps.DB}))
	r.Mount("/ws", wsroutes.Router(wsroutes.Deps{DB: deps.DB, Hub: deps.Hub, Logs: deps.Logs, Scheduler: deps.Scheduler, StartedAt: deps.StartedAt}))
	r.Mount("/z", z.Router())
	r.NotFound(spa(deps.FrontendRoot))
	return r
}

func spa(root string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(root))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, root+"/index.html")
			return
		}
		if _, err := http.Dir(root).Open(r.URL.Path); err == nil {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, root+"/index.html")
	}
}
