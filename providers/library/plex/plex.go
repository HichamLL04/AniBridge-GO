package plex

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	core  "anibridge-go/internal/core/providers"
)

// --- Provider ---

type Provider struct {
	namespace string
	url       string
	token     string
	libraries []string // library section IDs to scan
	clientID  string
	client    *http.Client
}

func init() { core.Register("library", "plex", New) }

func New(namespace string, settings map[string]any) (any, error) {
	p := &Provider{
		namespace: namespace,
		clientID:  "anibridge-go-" + namespace,
		client:    &http.Client{Timeout: 60 * time.Second},
	}
	if v, ok := settings["url"].(string); ok {
		p.url = strings.TrimRight(v, "/")
	}
	if v, ok := settings["token"].(string); ok {
		p.token = v
	}
	if v, ok := settings["client_id"].(string); ok {
		p.clientID = v
	}
	if v, ok := settings["libraries"]; ok {
		switch libs := v.(type) {
		case []any:
			for _, l := range libs {
				p.libraries = append(p.libraries, fmt.Sprintf("%v", l))
			}
		case string:
			for _, s := range strings.Split(libs, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					p.libraries = append(p.libraries, s)
				}
			}
		}
	}
	return p, nil
}

func (p *Provider) Namespace() string { return p.namespace }

func (p *Provider) Initialize(ctx context.Context) error {
	if p.url == "" {
		return fmt.Errorf("plex: url is required")
	}
	if p.token == "" {
		return fmt.Errorf("plex: token is required")
	}

	// Verify connectivity
	var identity struct {
		MediaContainer struct {
			MachineIdentifier string `xml:"machineIdentifier,attr" json:"machineIdentifier"`
			Version           string `xml:"version,attr" json:"version"`
		} `xml:"MediaContainer" json:"MediaContainer"`
	}
	if err := p.doGetXML(ctx, "/identity", &identity); err != nil {
		return fmt.Errorf("plex: failed to connect: %w", err)
	}
	slog.Info("plex: connected", "machine", identity.MediaContainer.MachineIdentifier, "version", identity.MediaContainer.Version)

	// Auto-discover anime libraries if none specified
	if len(p.libraries) == 0 {
		sections, err := p.getSections(ctx)
		if err != nil {
			return fmt.Errorf("plex: failed to list sections: %w", err)
		}
		for _, s := range sections {
			// Include "show" type libraries (anime is typically here)
			if s.Type == "show" {
				p.libraries = append(p.libraries, s.Key)
				slog.Info("plex: auto-detected library", "key", s.Key, "title", s.Title)
			}
		}
	}

	return nil
}

func (p *Provider) Scan(ctx context.Context) ([]core.MediaItem, error) {
	var items []core.MediaItem

	for _, sectionKey := range p.libraries {
		shows, err := p.scanSection(ctx, sectionKey)
		if err != nil {
			slog.Error("plex: scan section failed", "section", sectionKey, "error", err)
			continue
		}
		items = append(items, shows...)
	}

	slog.Info("plex: scan complete", "items", len(items))
	return items, nil
}

func (p *Provider) HandleWebhook(_ context.Context, body []byte) error {
	var wh plexWebhook
	if err := json.Unmarshal(body, &wh); err != nil {
		return fmt.Errorf("plex: invalid webhook payload: %w", err)
	}

	// Handle relevant events
	switch wh.Event {
	case "media.play", "media.resume":
		slog.Info("plex: webhook play",
			"title", wh.Metadata.GrandparentTitle,
			"episode", wh.Metadata.Title,
			"index", wh.Metadata.Index,
			"type", wh.Metadata.Type,
		)
	case "media.scrobble":
		slog.Info("plex: webhook scrobble",
			"title", wh.Metadata.GrandparentTitle,
			"episode", wh.Metadata.Title,
			"index", wh.Metadata.Index,
			"type", wh.Metadata.Type,
		)
	case "media.stop", "media.pause":
		slog.Debug("plex: webhook stop/pause", "event", wh.Event)
	default:
		slog.Debug("plex: webhook ignored", "event", wh.Event)
	}

	return nil
}

// --- Library scanning ---

func (p *Provider) getSections(ctx context.Context) ([]plexSection, error) {
	var container struct {
		MediaContainer struct {
			Directory []plexSection `xml:"Directory"`
		} `xml:"MediaContainer"`
	}
	if err := p.doGetXML(ctx, "/library/sections", &container); err != nil {
		return nil, err
	}
	return container.MediaContainer.Directory, nil
}

func (p *Provider) scanSection(ctx context.Context, sectionKey string) ([]core.MediaItem, error) {
	// Get all shows in section
	var container struct {
		MediaContainer struct {
			Metadata []plexShow `xml:"Metadata"`
		} `xml:"MediaContainer"`
	}
	if err := p.doGetXML(ctx, fmt.Sprintf("/library/sections/%s/all", sectionKey), &container); err != nil {
		return nil, err
	}

	var items []core.MediaItem
	for _, show := range container.MediaContainer.Metadata {
		// Get episode info for watched progress
		episodes, leafCount := p.getShowProgress(ctx, show.RatingKey)

		item := core.MediaItem{
			ID:       show.RatingKey,
			Title:    show.Title,
			Type:     "anime",
			Progress: episodes,
			Episodes: leafCount,
			ExternalID: map[string]string{
				"plex": show.RatingKey,
			},
		}

		// Determine status based on watch progress
		if episodes >= leafCount && leafCount > 0 {
			item.Status = "completed"
		} else if episodes > 0 {
			item.Status = "watching"
		} else {
			item.Status = "plan_to_watch"
		}

		// Extract external IDs from GUIDs
		for _, guid := range show.GUID {
			parts := strings.SplitN(guid.ID, "://", 2)
			if len(parts) == 2 {
				switch parts[0] {
				case "tvdb":
					item.ExternalID["tvdb"] = parts[1]
				case "tmdb":
					item.ExternalID["tmdb"] = parts[1]
				case "imdb":
					item.ExternalID["imdb"] = parts[1]
				case "anidb":
					item.ExternalID["anidb"] = parts[1]
				case "mal":
					item.ExternalID["mal"] = parts[1]
				case "anilist":
					item.ExternalID["anilist"] = parts[1]
				}
			}
		}

		if show.UpdatedAt > 0 {
			item.UpdatedAt = time.Unix(show.UpdatedAt, 0)
		}

		items = append(items, item)
	}
	return items, nil
}

func (p *Provider) getShowProgress(ctx context.Context, ratingKey string) (watched, total int) {
	var container struct {
		MediaContainer struct {
			LeafCount    int    `xml:"leafCount,attr"`
			ViewedLeafCount int `xml:"viewedLeafCount,attr"`
		} `xml:"MediaContainer"`
	}
	path := fmt.Sprintf("/library/metadata/%s", ratingKey)
	if err := p.doGetXML(ctx, path, &container); err != nil {
		return 0, 0
	}
	return container.MediaContainer.ViewedLeafCount, container.MediaContainer.LeafCount
}

// --- HTTP helpers ---

func (p *Provider) doGetXML(ctx context.Context, path string, out any) error {
	u := p.url + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Token", p.token)
	req.Header.Set("X-Plex-Client-Identifier", p.clientID)
	req.Header.Set("X-Plex-Product", "AniBridge GO")
	req.Header.Set("X-Plex-Version", "1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("plex: %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return xml.Unmarshal(body, out)
}

// --- Types ---

type plexSection struct {
	Key   string `xml:"key,attr"`
	Title string `xml:"title,attr"`
	Type  string `xml:"type,attr"`
}

type plexGUID struct {
	ID string `xml:"id,attr"`
}

type plexShow struct {
	RatingKey  string     `xml:"ratingKey,attr"`
	Title      string     `xml:"title,attr"`
	Year       int        `xml:"year,attr"`
	UpdatedAt  int64      `xml:"updatedAt,attr"`
	ViewCount  int        `xml:"viewCount,attr"`
	GUID       []plexGUID `xml:"Guid"`
}

type plexWebhook struct {
	Event   string `json:"event"`
	User    bool   `json:"user"`
	Owner   bool   `json:"owner"`
	Account struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	} `json:"Account"`
	Server struct {
		Title string `json:"title"`
		UUID  string `json:"uuid"`
	} `json:"Server"`
	Player struct {
		Title string `json:"title"`
		UUID  string `json:"uuid"`
	} `json:"Player"`
	Metadata struct {
		Type              string `json:"type"`
		Title             string `json:"title"`
		GrandparentTitle  string `json:"grandparentTitle"`
		ParentIndex       int    `json:"parentIndex"`
		Index             int    `json:"index"`
		RatingKey         string `json:"ratingKey"`
		Key               string `json:"key"`
		GUID              string `json:"guid"`
		ExternalGUID      []struct {
			ID string `json:"id"`
		} `json:"Guid"`
	} `json:"Metadata"`
}

func guidsToExternalIDs(guids []struct{ ID string }) map[string]string {
	out := map[string]string{}
	for _, g := range guids {
		parts := strings.SplitN(g.ID, "://", 2)
		if len(parts) == 2 {
			out[parts[0]] = parts[1]
		}
	}
	return out
}

// parseRatingKey extracts the numeric ID from a Plex rating key
func parseRatingKey(key string) int {
	// Rating keys are just numeric strings
	n, _ := strconv.Atoi(key)
	return n
}
