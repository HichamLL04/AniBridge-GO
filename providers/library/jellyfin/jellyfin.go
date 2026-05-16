package jellyfin

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

func init() { core.Register("library", "jellyfin", New) }

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
		return fmt.Errorf("jellyfin: url is required")
	}
	if p.apiKey == "" {
		return fmt.Errorf("jellyfin: api_key is required")
	}

	// Verify connectivity with server info
	var info struct {
		ServerName string `json:"ServerName"`
		Version    string `json:"Version"`
		ID         string `json:"Id"`
	}
	if err := p.doGet(ctx, "/System/Info/Public", nil, &info); err != nil {
		return fmt.Errorf("jellyfin: failed to connect: %w", err)
	}
	slog.Info("jellyfin: connected", "server", info.ServerName, "version", info.Version)

	// Auto-discover user if not specified
	if p.userID == "" {
		users, err := p.getUsers(ctx)
		if err != nil {
			return fmt.Errorf("jellyfin: failed to get users: %w", err)
		}
		if len(users) > 0 {
			p.userID = users[0].ID
			slog.Info("jellyfin: auto-selected user", "name", users[0].Name, "id", p.userID)
		} else {
			return fmt.Errorf("jellyfin: no users found")
		}
	}

	return nil
}

func (p *Provider) Scan(ctx context.Context) ([]core.MediaItem, error) {
	if p.userID == "" {
		return nil, fmt.Errorf("jellyfin: no user_id configured")
	}

	// Get anime series from library
	params := url.Values{
		"IncludeItemTypes": {"Series"},
		"Recursive":        {"true"},
		"Fields":           {"ProviderIds,Overview,Genres,Tags"},
		"Genres":           {"Anime"},
		"Limit":            {"10000"},
	}

	var result struct {
		Items      []jellyfinItem `json:"Items"`
		TotalCount int            `json:"TotalRecordCount"`
	}
	path := fmt.Sprintf("/Users/%s/Items", p.userID)
	if err := p.doGet(ctx, path, params, &result); err != nil {
		// If genre filter fails, try without it and filter by tags
		params.Del("Genres")
		if err := p.doGet(ctx, path, params, &result); err != nil {
			return nil, fmt.Errorf("jellyfin: scan failed: %w", err)
		}
	}

	var items []core.MediaItem
	for _, jf := range result.Items {
		// Filter for anime if we didn't use genre filter
		if !isAnime(jf) {
			continue
		}

		item := core.MediaItem{
			ID:         jf.ID,
			Title:      jf.Name,
			Type:       "anime",
			ExternalID: map[string]string{"jellyfin": jf.ID},
		}

		// Extract progress from user data
		if jf.UserData != nil {
			item.Progress = jf.UserData.UnplayedItemCount
			if jf.RecursiveItemCount > 0 {
				item.Progress = jf.RecursiveItemCount - jf.UserData.UnplayedItemCount
			}
		}
		item.Episodes = jf.RecursiveItemCount

		// Determine status
		if jf.UserData != nil && jf.UserData.Played {
			item.Status = "completed"
		} else if item.Progress > 0 {
			item.Status = "watching"
		} else {
			item.Status = "plan_to_watch"
		}

		// Extract provider IDs
		if jf.ProviderIDs.AniList != "" {
			item.ExternalID["anilist"] = jf.ProviderIDs.AniList
		}
		if jf.ProviderIDs.AniDB != "" {
			item.ExternalID["anidb"] = jf.ProviderIDs.AniDB
		}
		if jf.ProviderIDs.MAL != "" {
			item.ExternalID["mal"] = jf.ProviderIDs.MAL
		}
		if jf.ProviderIDs.TVDB != "" {
			item.ExternalID["tvdb"] = jf.ProviderIDs.TVDB
		}
		if jf.ProviderIDs.TMDB != "" {
			item.ExternalID["tmdb"] = jf.ProviderIDs.TMDB
		}
		if jf.ProviderIDs.IMDB != "" {
			item.ExternalID["imdb"] = jf.ProviderIDs.IMDB
		}

		if !jf.DateLastMediaAdded.IsZero() {
			item.UpdatedAt = jf.DateLastMediaAdded
		}

		items = append(items, item)
	}

	slog.Info("jellyfin: scan complete", "items", len(items))
	return items, nil
}

func (p *Provider) HandleWebhook(_ context.Context, body []byte) error {
	var wh jellyfinWebhook
	if err := json.Unmarshal(body, &wh); err != nil {
		return fmt.Errorf("jellyfin: invalid webhook payload: %w", err)
	}

	switch wh.NotificationType {
	case "PlaybackStart":
		slog.Info("jellyfin: playback start",
			"series", wh.SeriesName,
			"episode", wh.Name,
			"season", wh.SeasonNumber,
			"episode_num", wh.EpisodeNumber,
		)
	case "PlaybackStop":
		slog.Info("jellyfin: playback stop",
			"series", wh.SeriesName,
			"episode", wh.Name,
			"played_percent", wh.PlayedPercentage,
		)
	case "ItemAdded":
		slog.Info("jellyfin: item added", "name", wh.Name, "type", wh.ItemType)
	case "UserDataSaved":
		slog.Info("jellyfin: user data saved", "series", wh.SeriesName, "played", wh.Played)
	default:
		slog.Debug("jellyfin: webhook ignored", "type", wh.NotificationType)
	}

	return nil
}

// --- Helpers ---

func (p *Provider) getUsers(ctx context.Context) ([]jellyfinUser, error) {
	var users []jellyfinUser
	if err := p.doGet(ctx, "/Users", nil, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (p *Provider) doGet(ctx context.Context, path string, params url.Values, out any) error {
	u := p.url + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Token="%s"`, p.apiKey))

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jellyfin: %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func isAnime(item jellyfinItem) bool {
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
	// If it has AniDB or AniList provider IDs, it's likely anime
	if item.ProviderIDs.AniDB != "" || item.ProviderIDs.AniList != "" {
		return true
	}
	return false
}

// --- Types ---

type jellyfinUser struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type jellyfinProviderIDs struct {
	AniList string `json:"AniList"`
	AniDB   string `json:"AniDb"`
	MAL     string `json:"MyAnimeList"`
	TVDB    string `json:"Tvdb"`
	TMDB    string `json:"Tmdb"`
	IMDB    string `json:"Imdb"`
}

type jellyfinUserData struct {
	PlayedPercentage float64 `json:"PlayedPercentage"`
	UnplayedItemCount int    `json:"UnplayedItemCount"`
	Played           bool    `json:"Played"`
	PlayCount        int     `json:"PlayCount"`
	IsFavorite       bool    `json:"IsFavorite"`
}

type jellyfinItem struct {
	ID                 string              `json:"Id"`
	Name               string              `json:"Name"`
	Type               string              `json:"Type"`
	Genres             []string            `json:"Genres"`
	Tags               []string            `json:"Tags"`
	ProviderIDs        jellyfinProviderIDs `json:"ProviderIds"`
	UserData           *jellyfinUserData   `json:"UserData"`
	RecursiveItemCount int                 `json:"RecursiveItemCount"`
	DateLastMediaAdded time.Time           `json:"DateLastMediaAdded"`
	PremiereDate       time.Time           `json:"PremiereDate"`
}

type jellyfinWebhook struct {
	NotificationType string  `json:"NotificationType"`
	Name             string  `json:"Name"`
	SeriesName       string  `json:"SeriesName"`
	SeasonNumber     int     `json:"SeasonNumber"`
	EpisodeNumber    int     `json:"EpisodeNumber"`
	ItemType         string  `json:"ItemType"`
	Played           bool    `json:"Played"`
	PlayedPercentage float64 `json:"PlayedPercentage"`
	ItemID           string  `json:"ItemId"`
	ServerName       string  `json:"ServerName"`
	UserID           string  `json:"UserId"`
}
