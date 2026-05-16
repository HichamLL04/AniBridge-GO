package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	core  "anibridge-go/internal/core/providers"
)

// --- Provider ---

type Provider struct {
	namespace string
	url       string
	apiKey    string
	userID    string
	client    *http.Client
}

func init() { core.Register("library", "emby", New) }

func New(namespace string, settings map[string]any) (any, error) {
	p := &Provider{
		namespace: namespace,
		client:    &http.Client{Timeout: 60 * time.Second},
	}
	if v, ok := settings["url"].(string); ok {
		p.url = strings.TrimRight(v, "/")
	}
	if v, ok := settings["api_key"].(string); ok {
		p.apiKey = v
	}
	if v, ok := settings["user_id"].(string); ok {
		p.userID = v
	}
	return p, nil
}

func (p *Provider) Namespace() string { return p.namespace }

func (p *Provider) Initialize(ctx context.Context) error {
	if p.url == "" {
		return fmt.Errorf("emby: url is required")
	}
	if p.apiKey == "" {
		return fmt.Errorf("emby: api_key is required")
	}

	// Verify connectivity
	var info struct {
		ServerName string `json:"ServerName"`
		Version    string `json:"Version"`
		ID         string `json:"Id"`
	}
	if err := p.doGet(ctx, "/System/Info/Public", nil, &info); err != nil {
		return fmt.Errorf("emby: failed to connect: %w", err)
	}
	slog.Info("emby: connected", "server", info.ServerName, "version", info.Version)

	// Auto-discover user if not set
	if p.userID == "" {
		users, err := p.getUsers(ctx)
		if err != nil {
			return fmt.Errorf("emby: failed to get users: %w", err)
		}
		if len(users) > 0 {
			p.userID = users[0].ID
			slog.Info("emby: auto-selected user", "name", users[0].Name, "id", p.userID)
		} else {
			return fmt.Errorf("emby: no users found")
		}
	}

	return nil
}

func (p *Provider) Scan(ctx context.Context) ([]core.MediaItem, error) {
	if p.userID == "" {
		return nil, fmt.Errorf("emby: no user_id configured")
	}

	params := url.Values{
		"IncludeItemTypes": {"Series"},
		"Recursive":        {"true"},
		"Fields":           {"ProviderIds,Genres,Tags"},
		"Limit":            {"10000"},
	}

	var result struct {
		Items      []embyItem `json:"Items"`
		TotalCount int        `json:"TotalRecordCount"`
	}
	path := fmt.Sprintf("/Users/%s/Items", p.userID)
	if err := p.doGet(ctx, path, params, &result); err != nil {
		return nil, fmt.Errorf("emby: scan failed: %w", err)
	}

	var items []core.MediaItem
	for _, ei := range result.Items {
		if !isAnime(ei) {
			continue
		}

		item := core.MediaItem{
			ID:         ei.ID,
			Title:      ei.Name,
			Type:       "anime",
			ExternalID: map[string]string{"emby": ei.ID},
		}

		// Calculate progress from user data
		if ei.UserData != nil {
			if ei.RecursiveItemCount > 0 {
				item.Progress = ei.RecursiveItemCount - ei.UserData.UnplayedItemCount
			}
		}
		item.Episodes = ei.RecursiveItemCount

		// Status
		if ei.UserData != nil && ei.UserData.Played {
			item.Status = "completed"
		} else if item.Progress > 0 {
			item.Status = "watching"
		} else {
			item.Status = "plan_to_watch"
		}

		// Provider IDs
		if ei.ProviderIDs.AniList != "" {
			item.ExternalID["anilist"] = ei.ProviderIDs.AniList
		}
		if ei.ProviderIDs.AniDB != "" {
			item.ExternalID["anidb"] = ei.ProviderIDs.AniDB
		}
		if ei.ProviderIDs.MAL != "" {
			item.ExternalID["mal"] = ei.ProviderIDs.MAL
		}
		if ei.ProviderIDs.TVDB != "" {
			item.ExternalID["tvdb"] = ei.ProviderIDs.TVDB
		}
		if ei.ProviderIDs.TMDB != "" {
			item.ExternalID["tmdb"] = ei.ProviderIDs.TMDB
		}
		if ei.ProviderIDs.IMDB != "" {
			item.ExternalID["imdb"] = ei.ProviderIDs.IMDB
		}

		if !ei.DateLastMediaAdded.IsZero() {
			item.UpdatedAt = ei.DateLastMediaAdded
		}

		items = append(items, item)
	}

	slog.Info("emby: scan complete", "items", len(items))
	return items, nil
}

func (p *Provider) HandleWebhook(_ context.Context, body []byte) error {
	var wh embyWebhook
	if err := json.Unmarshal(body, &wh); err != nil {
		return fmt.Errorf("emby: invalid webhook payload: %w", err)
	}

	switch wh.Event {
	case "playback.start":
		slog.Info("emby: playback start",
			"series", wh.Item.SeriesName,
			"episode", wh.Item.Name,
			"season", wh.Item.ParentIndexNumber,
			"episode_num", wh.Item.IndexNumber,
		)
	case "playback.stop":
		slog.Info("emby: playback stop",
			"series", wh.Item.SeriesName,
			"episode", wh.Item.Name,
			"played_percent", wh.PlaybackInfo.PlayedPercentage,
		)
	case "item.markplayed":
		slog.Info("emby: marked played",
			"series", wh.Item.SeriesName,
			"episode", wh.Item.Name,
		)
	default:
		slog.Debug("emby: webhook ignored", "event", wh.Event)
	}

	return nil
}

// --- Helpers ---

func (p *Provider) getUsers(ctx context.Context) ([]embyUser, error) {
	var users []embyUser
	if err := p.doGet(ctx, "/Users", nil, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (p *Provider) doGet(ctx context.Context, path string, params url.Values, out any) error {
	u := p.url + path
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", p.apiKey)
	u += "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("emby: %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func isAnime(item embyItem) bool {
	for _, g := range item.Genres {
		lower := strings.ToLower(g)
		if lower == "anime" || lower == "animation" {
			return true
		}
	}
	for _, t := range item.Tags {
		if strings.ToLower(t) == "anime" {
			return true
		}
	}
	if item.ProviderIDs.AniDB != "" || item.ProviderIDs.AniList != "" {
		return true
	}
	return false
}

// --- Types ---

type embyUser struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type embyProviderIDs struct {
	AniList string `json:"AniList"`
	AniDB   string `json:"AniDb"`
	MAL     string `json:"MyAnimeList"`
	TVDB    string `json:"Tvdb"`
	TMDB    string `json:"Tmdb"`
	IMDB    string `json:"Imdb"`
}

type embyUserData struct {
	PlayedPercentage  float64 `json:"PlayedPercentage"`
	UnplayedItemCount int     `json:"UnplayedItemCount"`
	Played            bool    `json:"Played"`
	PlayCount         int     `json:"PlayCount"`
}

type embyItem struct {
	ID                 string          `json:"Id"`
	Name               string          `json:"Name"`
	Type               string          `json:"Type"`
	Genres             []string        `json:"Genres"`
	Tags               []string        `json:"Tags"`
	ProviderIDs        embyProviderIDs `json:"ProviderIds"`
	UserData           *embyUserData   `json:"UserData"`
	RecursiveItemCount int             `json:"RecursiveItemCount"`
	DateLastMediaAdded time.Time       `json:"DateLastMediaAdded"`
}

type embyWebhook struct {
	Event string `json:"Event"`
	Item  struct {
		Name              string `json:"Name"`
		SeriesName        string `json:"SeriesName"`
		Type              string `json:"Type"`
		ParentIndexNumber int    `json:"ParentIndexNumber"`
		IndexNumber       int    `json:"IndexNumber"`
		ID                string `json:"Id"`
		ProviderIDs       struct {
			AniDB   string `json:"AniDb"`
			AniList string `json:"AniList"`
		} `json:"ProviderIds"`
	} `json:"Item"`
	PlaybackInfo struct {
		PlayedPercentage float64 `json:"PlayedPercentage"`
		PositionTicks    int64   `json:"PositionTicks"`
	} `json:"PlaybackInfo"`
	User struct {
		Name string `json:"Name"`
		ID   string `json:"Id"`
	} `json:"User"`
}
