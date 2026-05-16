package simkl

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

const apiBase = "https://api.simkl.com"

// --- Status mapping ---

var statusToSimkl = map[string]string{
	"watching":      "watching",
	"current":       "watching",
	"plan_to_watch": "plantowatch",
	"planning":      "plantowatch",
	"completed":     "completed",
	"dropped":       "dropped",
	"paused":        "hold",
	"on_hold":       "hold",
	"repeating":     "watching",
}

var statusFromSimkl = map[string]string{
	"watching":    "watching",
	"plantowatch": "plan_to_watch",
	"completed":   "completed",
	"dropped":     "dropped",
	"hold":        "paused",
	"notinteresting": "dropped",
}

// --- Provider ---

type Provider struct {
	namespace   string
	clientID    string
	accessToken string
	client      *http.Client
}

func init() { core.Register("list", "simkl", New) }

func New(namespace string, settings map[string]any) (any, error) {
	p := &Provider{
		namespace: namespace,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
	if v, ok := settings["client_id"].(string); ok {
		p.clientID = v
	}
	if v, ok := settings["access_token"].(string); ok {
		p.accessToken = v
	}
	return p, nil
}

func (p *Provider) Namespace() string { return p.namespace }

func (p *Provider) Initialize(ctx context.Context) error {
	if p.clientID == "" {
		slog.Warn("simkl: no client_id configured")
		return nil
	}
	if p.accessToken == "" {
		slog.Warn("simkl: no access_token configured, provider is read-only")
		return nil
	}
	// Verify token by fetching user settings
	var user struct {
		Account struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		} `json:"account"`
		User struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	if err := p.doGet(ctx, "/users/settings", &user); err != nil {
		return fmt.Errorf("simkl: failed to verify token: %w", err)
	}
	slog.Info("simkl: authenticated", "user", user.User.Name)
	return nil
}

func (p *Provider) GetEntry(ctx context.Context, anilistID int64) (*core.ListEntry, error) {
	// Simkl can look up by AniList ID via their all-items endpoint
	// We fetch the full anime list and search for the matching anilist ID
	var items []simklAnimeItem
	if err := p.doGet(ctx, "/sync/all-items/anime", &items); err != nil {
		return nil, fmt.Errorf("simkl: get all items: %w", err)
	}

	for _, item := range items {
		// Check if this item's AniList ID matches
		if item.Show.IDs.AniList == int(anilistID) {
			entry := &core.ListEntry{
				ID:        strconv.Itoa(item.Show.IDs.Simkl),
				AniListID: anilistID,
				Status:    statusFromSimkl[item.Status],
				Progress:  item.TotalEpisodesCount,
			}
			if item.UserRating != nil {
				r := float64(*item.UserRating)
				entry.UserRating = &r
			}
			if item.LastWatchedAt != "" {
				if t, err := time.Parse(time.RFC3339, item.LastWatchedAt); err == nil {
					entry.FinishedAt = &t
				}
			}
			return entry, nil
		}
	}
	return nil, nil
}

func (p *Provider) UpdateEntry(ctx context.Context, entry core.ListEntry, fields []core.SyncField, dryRun bool) error {
	if p.accessToken == "" {
		return fmt.Errorf("simkl: cannot update without access_token")
	}

	// Determine target status
	targetStatus := "watching"
	for _, f := range fields {
		if f == core.FieldStatus {
			if s, ok := statusToSimkl[entry.Status]; ok {
				targetStatus = s
			}
			break
		}
	}

	// Build the show object with AniList ID
	show := simklShowIDs{
		IDs: simklIDs{AniList: int(entry.AniListID)},
	}

	payload := simklSyncPayload{
		Shows: []simklSyncShow{
			{
				To:            targetStatus,
				Title:         "",
				IDs:           show.IDs,
				WatchedAt:     time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	// Set rating if requested
	for _, f := range fields {
		if f == core.FieldUserRating && entry.UserRating != nil {
			payload.Shows[0].Rating = entry.UserRating
		}
	}

	// Set episodes watched
	for _, f := range fields {
		if f == core.FieldProgress {
			payload.Shows[0].EpisodesWatched = entry.Progress
		}
	}

	if dryRun {
		b, _ := json.Marshal(payload)
		slog.Info("simkl: dry-run update", "anilist_id", entry.AniListID, "payload", string(b))
		return nil
	}

	return p.doPost(ctx, "/sync/add-to-list", payload)
}

// --- HTTP helpers ---

func (p *Provider) doGet(ctx context.Context, path string, out any) error {
	u := apiBase + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("simkl-api-key", p.clientID)
	if p.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.accessToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("simkl: %s returned %d: %s", path, resp.StatusCode, string(body))
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
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("simkl-api-key", p.clientID)
	if p.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.accessToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("simkl: %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	slog.Info("simkl: sync successful", "path", path)
	return nil
}

// --- Types ---

type simklIDs struct {
	Simkl   int `json:"simkl,omitempty"`
	AniList int `json:"anilist,omitempty"`
	MAL     int `json:"mal,omitempty"`
	AniDB   int `json:"anidb,omitempty"`
}

type simklShowIDs struct {
	IDs simklIDs `json:"ids"`
}

type simklAnimeItem struct {
	Show struct {
		Title string   `json:"title"`
		IDs   simklIDs `json:"ids"`
	} `json:"show"`
	Status             string   `json:"status"`
	UserRating         *int     `json:"user_rating"`
	TotalEpisodesCount int      `json:"total_episodes_count"`
	LastWatchedAt      string   `json:"last_watched_at"`
}

type simklSyncShow struct {
	To              string   `json:"to"`
	Title           string   `json:"title,omitempty"`
	IDs             simklIDs `json:"ids"`
	WatchedAt       string   `json:"watched_at,omitempty"`
	Rating          *float64 `json:"rating,omitempty"`
	EpisodesWatched int      `json:"episodes_watched,omitempty"`
}

type simklSyncPayload struct {
	Shows []simklSyncShow `json:"shows"`
}
