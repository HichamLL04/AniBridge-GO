# AniBridge GO

AniBridge GO is a Go rewrite scaffold for AniBridge, built to keep idle memory low by replacing the Python FastAPI/SQLAlchemy/Alembic backend with:

- `chi` for routing
- `database/sql` plus SQLite
- direct migrations
- `slog`
- goroutine-based profile scheduling
- WebSockets via Gorilla

The static frontend is served from `frontend/build/`. Copy the original AniBridge Svelte build into that directory to preserve the UI unchanged.

## Run

```bash
go mod tidy
go run ./cmd/anibridge-go -config config.yml
```

Health checks:

```bash
curl http://localhost:8080/z/livez
curl http://localhost:8080/api/system
```

## Status

This repository contains the backend architecture, database tables, config validation, scheduler, endpoint surface, WebSocket hub, provider interfaces and provider registrations for Plex, Jellyfin, Emby, AniList, MAL, Simkl and Trakt.

Provider API calls and the exact AniBridge sync semantics are intentionally isolated behind the provider and sync engine interfaces so they can be filled in without changing the web/API contract.
