package ws

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	 "anibridge-go/internal/core/sched"
	 "anibridge-go/internal/web/services"
)

type Deps struct {
	DB        *sql.DB
	Hub       *services.Hub
	Logs      *services.LogStore
	Scheduler *sched.Client
	StartedAt time.Time
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func Router(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Get("/history", d.stream("history"))
	r.Get("/logs", d.stream("logs"))
	r.Get("/status", d.stream("status"))
	return r
}

func (d Deps) stream(topic string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if topic == "logs" {
			for _, l := range d.Logs.List() {
				if err := conn.WriteJSON(l); err != nil {
					return
				}
			}
		}

		ch, unsubscribe := d.Hub.Subscribe(topic)
		defer unsubscribe()
		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-ch:
				if err := conn.WriteJSON(msg); err != nil {
					return
				}
			}
		}
	}
}
