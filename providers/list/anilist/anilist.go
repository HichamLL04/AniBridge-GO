package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	core  "anibridge-go/internal/core/providers"
)

const graphqlURL = "https://graphql.anilist.co"

// --- GraphQL queries and mutations ---

const queryMediaList = `
query ($mediaId: Int) {
  MediaList(mediaId: $mediaId) {
    id
    mediaId
    status
    progress
    repeat
    notes
    score(format: POINT_10_DECIMAL)
    startedAt { year month day }
    completedAt { year month day }
    updatedAt
  }
}
`

const queryMediaListByUser = `
query ($userId: Int, $mediaId: Int) {
  MediaList(userId: $userId, mediaId: $mediaId) {
    id
    mediaId
    status
    progress
    repeat
    notes
    score(format: POINT_10_DECIMAL)
    startedAt { year month day }
    completedAt { year month day }
    updatedAt
  }
}
`

const mutationSaveMediaListEntry = `
mutation ($mediaId: Int, $status: MediaListStatus, $progress: Int, $repeat: Int, $notes: String, $scoreRaw: Int, $startedAt: FuzzyDateInput, $completedAt: FuzzyDateInput) {
  SaveMediaListEntry(mediaId: $mediaId, status: $status, progress: $progress, repeat: $repeat, notes: $notes, scoreRaw: $scoreRaw, startedAt: $startedAt, completedAt: $completedAt) {
    id
    mediaId
    status
    progress
    repeat
  }
}
`

const queryViewer = `
query {
  Viewer {
    id
    name
  }
}
`

// --- AniList status constants ---

var statusToAniList = map[string]string{
	"watching":      "CURRENT",
	"current":       "CURRENT",
	"plan_to_watch": "PLANNING",
	"planning":      "PLANNING",
	"completed":     "COMPLETED",
	"dropped":       "DROPPED",
	"paused":        "PAUSED",
	"on_hold":       "PAUSED",
	"repeating":     "REPEATING",
}

var statusFromAniList = map[string]string{
	"CURRENT":   "watching",
	"PLANNING":  "plan_to_watch",
	"COMPLETED": "completed",
	"DROPPED":   "dropped",
	"PAUSED":    "paused",
	"REPEATING": "repeating",
}

// --- Provider implementation ---

type Provider struct {
	namespace string
	token     string
	userID    int64
	client    *http.Client
}

func init() { core.Register("list", "anilist", New) }

func New(namespace string, settings map[string]any) (any, error) {
	p := &Provider{
		namespace: namespace,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
	if t, ok := settings["token"].(string); ok {
		p.token = t
	}
	return p, nil
}

func (p *Provider) Namespace() string { return p.namespace }

func (p *Provider) Initialize(ctx context.Context) error {
	if p.token == "" {
		slog.Warn("anilist: no token configured, provider will be read-only")
		return nil
	}
	// Fetch the authenticated user to get userID
	var resp struct {
		Data struct {
			Viewer struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"Viewer"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := p.graphql(ctx, queryViewer, nil, &resp); err != nil {
		return fmt.Errorf("anilist: failed to fetch viewer: %w", err)
	}
	if len(resp.Errors) > 0 {
		return fmt.Errorf("anilist: API error: %s", resp.Errors[0].Message)
	}
	p.userID = resp.Data.Viewer.ID
	slog.Info("anilist: authenticated", "user", resp.Data.Viewer.Name, "id", p.userID)
	return nil
}

func (p *Provider) GetEntry(ctx context.Context, anilistID int64) (*core.ListEntry, error) {
	vars := map[string]any{"mediaId": anilistID}
	if p.userID > 0 {
		vars["userId"] = p.userID
	}

	query := queryMediaList
	if p.userID > 0 {
		query = queryMediaListByUser
	}

	var resp struct {
		Data struct {
			MediaList *mediaListResponse `json:"MediaList"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := p.graphql(ctx, query, vars, &resp); err != nil {
		return nil, fmt.Errorf("anilist: get entry %d: %w", anilistID, err)
	}
	if len(resp.Errors) > 0 {
		// "Not Found" is normal for entries not on the user's list
		for _, e := range resp.Errors {
			if e.Status == 404 {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("anilist: API error: %s", resp.Errors[0].Message)
	}
	if resp.Data.MediaList == nil {
		return nil, nil
	}

	ml := resp.Data.MediaList
	entry := &core.ListEntry{
		ID:        fmt.Sprintf("%d", ml.ID),
		AniListID: int64(ml.MediaID),
		Status:    statusFromAniList[ml.Status],
		Progress:  ml.Progress,
		Repeats:   ml.Repeat,
		Review:    ml.Notes,
	}
	if ml.Score > 0 {
		s := ml.Score
		entry.UserRating = &s
	}
	if t := fuzzyDateToTime(ml.StartedAt); t != nil {
		entry.StartedAt = t
	}
	if t := fuzzyDateToTime(ml.CompletedAt); t != nil {
		entry.FinishedAt = t
	}
	return entry, nil
}

func (p *Provider) UpdateEntry(ctx context.Context, entry core.ListEntry, fields []core.SyncField, dryRun bool) error {
	if p.token == "" {
		return fmt.Errorf("anilist: cannot update without a token")
	}

	vars := map[string]any{
		"mediaId": entry.AniListID,
	}

	for _, f := range fields {
		switch f {
		case core.FieldStatus:
			if s, ok := statusToAniList[entry.Status]; ok {
				vars["status"] = s
			}
		case core.FieldProgress:
			vars["progress"] = entry.Progress
		case core.FieldRepeats:
			vars["repeat"] = entry.Repeats
		case core.FieldReview:
			vars["notes"] = entry.Review
		case core.FieldUserRating:
			if entry.UserRating != nil {
				// AniList uses scoreRaw (0-100) — we store 0-10 float
				vars["scoreRaw"] = int(*entry.UserRating * 10)
			}
		case core.FieldStartedAt:
			if entry.StartedAt != nil {
				vars["startedAt"] = timeToFuzzyDate(*entry.StartedAt)
			}
		case core.FieldFinishedAt:
			if entry.FinishedAt != nil {
				vars["completedAt"] = timeToFuzzyDate(*entry.FinishedAt)
			}
		}
	}

	if dryRun {
		slog.Info("anilist: dry-run update", "anilist_id", entry.AniListID, "vars", vars)
		return nil
	}

	var resp struct {
		Data struct {
			SaveMediaListEntry struct {
				ID int `json:"id"`
			} `json:"SaveMediaListEntry"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := p.graphql(ctx, mutationSaveMediaListEntry, vars, &resp); err != nil {
		return fmt.Errorf("anilist: update entry %d: %w", entry.AniListID, err)
	}
	if len(resp.Errors) > 0 {
		return fmt.Errorf("anilist: mutation error: %s", resp.Errors[0].Message)
	}
	slog.Info("anilist: entry updated", "anilist_id", entry.AniListID, "entry_id", resp.Data.SaveMediaListEntry.ID)
	return nil
}

// --- GraphQL transport ---

func (p *Provider) graphql(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Handle rate limiting
	if resp.StatusCode == 429 {
		slog.Warn("anilist: rate limited, backing off")
		return fmt.Errorf("anilist: rate limited (429)")
	}

	return json.Unmarshal(respBody, out)
}

// --- Types ---

type gqlError struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
}

type fuzzyDate struct {
	Year  *int `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

type mediaListResponse struct {
	ID          int       `json:"id"`
	MediaID     int       `json:"mediaId"`
	Status      string    `json:"status"`
	Progress    int       `json:"progress"`
	Repeat      int       `json:"repeat"`
	Notes       string    `json:"notes"`
	Score       float64   `json:"score"`
	StartedAt   fuzzyDate `json:"startedAt"`
	CompletedAt fuzzyDate `json:"completedAt"`
	UpdatedAt   int64     `json:"updatedAt"`
}

func fuzzyDateToTime(fd fuzzyDate) *time.Time {
	if fd.Year == nil || *fd.Year == 0 {
		return nil
	}
	y := *fd.Year
	m := 1
	d := 1
	if fd.Month != nil && *fd.Month > 0 {
		m = *fd.Month
	}
	if fd.Day != nil && *fd.Day > 0 {
		d = *fd.Day
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return &t
}

func timeToFuzzyDate(t time.Time) map[string]int {
	return map[string]int{
		"year":  t.Year(),
		"month": int(t.Month()),
		"day":   t.Day(),
	}
}
