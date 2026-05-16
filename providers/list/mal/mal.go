package mal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	core  "anibridge-go/internal/core/providers"
)

const (
	apiBase = "https://api.myanimelist.net/v2"
)

// --- MAL status mapping ---

var statusToMAL = map[string]string{
	"watching":      "watching",
	"current":       "watching",
	"plan_to_watch": "plan_to_watch",
	"planning":      "plan_to_watch",
	"completed":     "completed",
	"dropped":       "dropped",
	"paused":        "on_hold",
	"on_hold":       "on_hold",
	"repeating":     "watching",
}

var statusFromMAL = map[string]string{
	"watching":      "watching",
	"plan_to_watch": "plan_to_watch",
	"completed":     "completed",
	"dropped":       "dropped",
	"on_hold":       "paused",
}

// --- Provider ---

type Provider struct {
	namespace    string
	clientID     string
	accessToken  string
	refreshToken string
	client       *http.Client
}

func init() { core.Register("list", "mal", New) }

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
	if v, ok := settings["refresh_token"].(string); ok {
		p.refreshToken = v
	}
	return p, nil
}

func (p *Provider) Namespace() string { return p.namespace }

func (p *Provider) Initialize(ctx context.Context) error {
	if p.accessToken == "" {
		slog.Warn("mal: no access_token configured, provider will not function")
		return nil
	}
	// Verify the token by fetching user info
	var user struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := p.doGet(ctx, "/users/@me", nil, &user); err != nil {
		return fmt.Errorf("mal: failed to verify token: %w", err)
	}
	slog.Info("mal: authenticated", "user", user.Name, "id", user.ID)
	return nil
}

func (p *Provider) GetEntry(ctx context.Context, anilistID int64) (*core.ListEntry, error) {
	// MAL uses its own IDs, but AniBridge maps anilist_id → mal_id through the animap table.
	// Here anilistID is actually passed as MAL anime ID (mapped externally by the sync engine).
	// The sync engine should provide the correct MAL ID via the animap.
	malID := anilistID

	var resp struct {
		ID               int    `json:"id"`
		Title            string `json:"title"`
		MyListStatus     *malListStatus `json:"my_list_status"`
	}

	params := url.Values{"fields": {"my_list_status{start_date,finish_date,num_times_rewatched,comments,score}"}}
	if err := p.doGet(ctx, fmt.Sprintf("/anime/%d", malID), params, &resp); err != nil {
		// 404 means not on list
		return nil, nil
	}
	if resp.MyListStatus == nil {
		return nil, nil
	}

	ms := resp.MyListStatus
	entry := &core.ListEntry{
		ID:        fmt.Sprintf("%d", resp.ID),
		AniListID: anilistID,
		Status:    statusFromMAL[ms.Status],
		Progress:  ms.NumEpisodesWatched,
		Repeats:   ms.NumTimesRewatched,
		Review:    ms.Comments,
	}
	if ms.Score > 0 {
		s := float64(ms.Score)
		entry.UserRating = &s
	}
	if ms.StartDate != "" {
		if t, err := time.Parse("2006-01-02", ms.StartDate); err == nil {
			entry.StartedAt = &t
		}
	}
	if ms.FinishDate != "" {
		if t, err := time.Parse("2006-01-02", ms.FinishDate); err == nil {
			entry.FinishedAt = &t
		}
	}
	return entry, nil
}

func (p *Provider) UpdateEntry(ctx context.Context, entry core.ListEntry, fields []core.SyncField, dryRun bool) error {
	if p.accessToken == "" {
		return fmt.Errorf("mal: cannot update without access_token")
	}

	malID := entry.AniListID
	form := url.Values{}

	for _, f := range fields {
		switch f {
		case core.FieldStatus:
			if s, ok := statusToMAL[entry.Status]; ok {
				form.Set("status", s)
			}
		case core.FieldProgress:
			form.Set("num_watched_episodes", strconv.Itoa(entry.Progress))
		case core.FieldRepeats:
			form.Set("num_times_rewatched", strconv.Itoa(entry.Repeats))
		case core.FieldReview:
			form.Set("comments", entry.Review)
		case core.FieldUserRating:
			if entry.UserRating != nil {
				// MAL uses integer 0-10
				form.Set("score", strconv.Itoa(int(*entry.UserRating)))
			}
		case core.FieldStartedAt:
			if entry.StartedAt != nil {
				form.Set("start_date", entry.StartedAt.Format("2006-01-02"))
			}
		case core.FieldFinishedAt:
			if entry.FinishedAt != nil {
				form.Set("finish_date", entry.FinishedAt.Format("2006-01-02"))
			}
		}
	}

	if len(form) == 0 {
		return nil
	}

	if dryRun {
		slog.Info("mal: dry-run update", "mal_id", malID, "fields", form.Encode())
		return nil
	}

	endpoint := fmt.Sprintf("%s/anime/%d/my_list_status", apiBase, malID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.accessToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("mal: update %d: %w", malID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mal: update %d returned %d: %s", malID, resp.StatusCode, string(body))
	}

	slog.Info("mal: entry updated", "mal_id", malID)
	return nil
}

// --- HTTP helpers ---

func (p *Provider) doGet(ctx context.Context, path string, params url.Values, out any) error {
	u := apiBase + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if p.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.accessToken)
	} else if p.clientID != "" {
		req.Header.Set("X-MAL-CLIENT-ID", p.clientID)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return fmt.Errorf("not found")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mal: %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// --- Types ---

type malListStatus struct {
	Status             string `json:"status"`
	Score              int    `json:"score"`
	NumEpisodesWatched int    `json:"num_episodes_watched"`
	NumTimesRewatched  int    `json:"num_times_rewatched"`
	StartDate          string `json:"start_date"`
	FinishDate         string `json:"finish_date"`
	Comments           string `json:"comments"`
	UpdatedAt          string `json:"updated_at"`
}
