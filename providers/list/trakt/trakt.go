package trakt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	core  "anibridge-go/internal/core/providers"
)

const apiBase = "https://api.trakt.tv"

// --- Status mapping ---

var statusToTrakt = map[string]string{
	"watching":      "watching",
	"current":       "watching",
	"plan_to_watch": "watchlist",
	"planning":      "watchlist",
	"completed":     "completed",
	"dropped":       "dropped",
	"paused":        "paused",
	"on_hold":       "paused",
	"repeating":     "watching",
}

var statusFromTrakt = map[string]string{
	"watching":  "watching",
	"watchlist": "plan_to_watch",
	"completed": "completed",
	"dropped":   "dropped",
	"paused":    "paused",
}

// --- Provider ---

type Provider struct {
	namespace    string
	clientID     string
	clientSecret string
	accessToken  string
	client       *http.Client
}

func init() { core.Register("list", "trakt", New) }

func New(namespace string, settings map[string]any) (any, error) {
	p := &Provider{
		namespace: namespace,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
	if v, ok := settings["client_id"].(string); ok {
		p.clientID = v
	}
	if v, ok := settings["client_secret"].(string); ok {
		p.clientSecret = v
	}
	if v, ok := settings["access_token"].(string); ok {
		p.accessToken = v
	}
	return p, nil
}

func (p *Provider) Namespace() string { return p.namespace }

func (p *Provider) Initialize(ctx context.Context) error {
	if p.clientID == "" {
		slog.Warn("trakt: no client_id configured")
		return nil
	}
	if p.accessToken == "" {
		slog.Warn("trakt: no access_token configured, provider is read-only")
		return nil
	}
	// Verify by fetching user settings
	var user struct {
		User struct {
			Username string `json:"username"`
			IDs      struct {
				Slug string `json:"slug"`
			} `json:"ids"`
		} `json:"user"`
	}
	if err := p.doGet(ctx, "/users/settings", &user); err != nil {
		return fmt.Errorf("trakt: failed to verify token: %w", err)
	}
	slog.Info("trakt: authenticated", "user", user.User.Username)
	return nil
}

func (p *Provider) GetEntry(ctx context.Context, anilistID int64) (*core.ListEntry, error) {
	// Trakt doesn't use AniList IDs natively — the sync engine maps them.
	// Here anilistID is treated as the Trakt show ID (mapped externally).
	traktID := anilistID

	// First check watched progress
	var progress struct {
		Aired     int  `json:"aired"`
		Completed int  `json:"completed"`
		LastWatchedAt string `json:"last_watched_at"`
		Show      struct {
			Title string   `json:"title"`
			IDs   traktIDs `json:"ids"`
		} `json:"show"`
	}
	err := p.doGet(ctx, fmt.Sprintf("/shows/%d/progress/watched", traktID), &progress)
	if err != nil {
		// Try to find in watchlist instead
		return p.getFromWatchlist(ctx, traktID, anilistID)
	}

	entry := &core.ListEntry{
		ID:        strconv.FormatInt(traktID, 10),
		AniListID: anilistID,
		Progress:  progress.Completed,
	}

	// Determine status based on progress
	if progress.Completed >= progress.Aired && progress.Aired > 0 {
		entry.Status = "completed"
	} else if progress.Completed > 0 {
		entry.Status = "watching"
	} else {
		entry.Status = "plan_to_watch"
	}

	if progress.LastWatchedAt != "" {
		if t, err := time.Parse(time.RFC3339, progress.LastWatchedAt); err == nil {
			entry.FinishedAt = &t
		}
	}

	// Get rating
	var ratings []struct {
		Rating int `json:"rating"`
		Show   struct {
			IDs traktIDs `json:"ids"`
		} `json:"show"`
	}
	if err := p.doGet(ctx, "/sync/ratings/shows", &ratings); err == nil {
		for _, r := range ratings {
			if int64(r.Show.IDs.Trakt) == traktID {
				rating := float64(r.Rating)
				entry.UserRating = &rating
				break
			}
		}
	}

	return entry, nil
}

func (p *Provider) getFromWatchlist(ctx context.Context, traktID, anilistID int64) (*core.ListEntry, error) {
	var items []struct {
		Show struct {
			Title string   `json:"title"`
			IDs   traktIDs `json:"ids"`
		} `json:"show"`
		ListedAt string `json:"listed_at"`
	}
	if err := p.doGet(ctx, "/sync/watchlist/shows", &items); err != nil {
		return nil, nil
	}
	for _, item := range items {
		if int64(item.Show.IDs.Trakt) == traktID {
			return &core.ListEntry{
				ID:        strconv.FormatInt(traktID, 10),
				AniListID: anilistID,
				Status:    "plan_to_watch",
				Progress:  0,
			}, nil
		}
	}
	return nil, nil
}

func (p *Provider) UpdateEntry(ctx context.Context, entry core.ListEntry, fields []core.SyncField, dryRun bool) error {
	if p.accessToken == "" {
		return fmt.Errorf("trakt: cannot update without access_token")
	}

	show := traktShow{
		IDs: traktIDs{Trakt: int(entry.AniListID)},
	}

	for _, f := range fields {
		switch f {
		case core.FieldStatus:
			status := statusToTrakt[entry.Status]
			if dryRun {
				slog.Info("trakt: dry-run status update", "trakt_id", entry.AniListID, "status", status)
				continue
			}
			switch status {
			case "watchlist":
				payload := map[string]any{"shows": []traktShow{show}}
				if err := p.doPost(ctx, "/sync/watchlist", payload); err != nil {
					return err
				}
			case "completed":
				payload := map[string]any{"shows": []map[string]any{
					{"ids": show.IDs, "watched_at": time.Now().UTC().Format(time.RFC3339)},
				}}
				if err := p.doPost(ctx, "/sync/history", payload); err != nil {
					return err
				}
			}

		case core.FieldProgress:
			if dryRun {
				slog.Info("trakt: dry-run progress update", "trakt_id", entry.AniListID, "progress", entry.Progress)
				continue
			}
			// Scrobble episodes up to the progress count
			episodes := make([]map[string]any, 0, entry.Progress)
			for i := 1; i <= entry.Progress; i++ {
				episodes = append(episodes, map[string]any{
					"watched_at": time.Now().UTC().Format(time.RFC3339),
					"number":     i,
				})
			}
			if len(episodes) > 0 {
				payload := map[string]any{
					"episodes": episodes,
				}
				if err := p.doPost(ctx, "/sync/history", payload); err != nil {
					return err
				}
			}

		case core.FieldUserRating:
			if entry.UserRating != nil {
				if dryRun {
					slog.Info("trakt: dry-run rating update", "trakt_id", entry.AniListID, "rating", *entry.UserRating)
					continue
				}
				payload := map[string]any{
					"shows": []map[string]any{
						{"ids": show.IDs, "rating": int(*entry.UserRating)},
					},
				}
				if err := p.doPost(ctx, "/sync/ratings", payload); err != nil {
					return err
				}
			}
		}
	}

	slog.Info("trakt: entry updated", "trakt_id", entry.AniListID)
	return nil
}

// --- HTTP helpers ---

func (p *Provider) doGet(ctx context.Context, path string, out any) error {
	u := apiBase + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("trakt: %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (p *Provider) doPost(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	u := apiBase + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("trakt: %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	slog.Debug("trakt: post successful", "path", path)
	return nil
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", p.clientID)
	if p.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.accessToken)
	}
}

// --- Types ---

type traktIDs struct {
	Trakt int `json:"trakt,omitempty"`
	Slug  string `json:"slug,omitempty"`
	IMDB  string `json:"imdb,omitempty"`
	TMDB  int    `json:"tmdb,omitempty"`
}

type traktShow struct {
	Title string   `json:"title,omitempty"`
	IDs   traktIDs `json:"ids"`
}
