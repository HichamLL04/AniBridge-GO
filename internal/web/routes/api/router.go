package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	 "anibridge-go/internal/config"
	 "anibridge-go/internal/core/animap"
	 "anibridge-go/internal/core/sched"
	 "anibridge-go/internal/models/schemas"
	 "anibridge-go/internal/utils"
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

func Router(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Get("/config", d.getConfig)
	r.Post("/config", d.postConfig)
	r.Get("/config/schema", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, config.JSONSchema()) })

	r.Get("/status", d.status)
	r.Post("/sync", d.syncAll)
	r.Post("/sync/database", d.syncDatabase)
	r.Post("/sync/profile/{profile}", d.syncProfile)
	r.Post("/sync/profile/{profile}/reinitialize", d.syncOk)

	r.Get("/mappings", d.listMappings)
	r.Get("/mappings/query-capabilities", d.mappingQueryCapabilities)
	r.Get("/mappings/{descriptor}", d.getMapping)
	r.Post("/mappings", d.upsertMapping)
	r.Put("/mappings/{descriptor}", d.upsertMapping)
	r.Delete("/mappings/{descriptor}", d.deleteMapping)

	r.Get("/pins/fields", d.listPins) // Dummy redirect
	r.Get("/pins/{profile}", d.listPins)
	r.Get("/pins/{profile}/{mediaKey}", d.listPins) // Dummy
	r.Post("/pins", d.upsertPin)
	r.Delete("/pins", d.deletePin)

	r.Get("/history", d.historyAll)
	r.Get("/history/{profile}", d.historyProfile)
	r.Get("/history/{profile}/{id}", d.historySingle)
	r.Post("/history/{profile}/{id}/undo", d.syncOk)
	r.Post("/history/{profile}/{id}/retry", d.syncOk)

	r.Get("/logs/files", d.logsFiles)
	r.Get("/logs/file/{name}", d.logsFile)

	r.Get("/backups/{profile}", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"items": []any{}}) })
	r.Post("/backups/{profile}/restore", d.syncOk)

	r.Get("/system/about", d.systemAbout)
	r.Get("/system/meta", d.systemMeta)
	r.Post("/system/restart", d.syncOk)

	return r
}

func (d Deps) getConfig(w http.ResponseWriter, r *http.Request) {
	b, err := yaml.Marshal(d.Config)
	if err != nil {
		writeErr(w, err, 500)
		return
	}
	writeJSON(w, map[string]any{
		"config_path": d.ConfigPath,
		"file_exists": true,
		"content":     string(b),
	})
}

func (d Deps) postConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err, 400)
		return
	}
	cfg := config.Default()
	if err := yaml.Unmarshal([]byte(req.Content), &cfg); err != nil {
		writeErr(w, err, 400)
		return
	}
	if err := config.Save(d.ConfigPath, cfg); err != nil {
		writeErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) status(w http.ResponseWriter, r *http.Request) {
	st := d.Scheduler.Status()

	// Convert internal status to ProfileStatusModel expected by Svelte
	profiles := make(map[string]schemas.ProfileStatusModel)
	for name, p := range st.Profiles {

		// Find profile config
		var libNs, listNs string
		for _, cfgProfile := range d.Config.Profiles {
			if cfgProfile.Name == name {
				libNs = cfgProfile.LibraryProvider
				listNs = cfgProfile.ListProvider
				break
			}
		}

		profiles[name] = schemas.ProfileStatusModel{
			Config: schemas.ProfileConfigModel{
				LibraryNamespace: libNs,
				ListNamespace:    listNs,
				ScanModes:        []string{"periodic"}, // Dummy mode
			},
			Status: schemas.ProfileRuntimeStatusModel{
				Running:  p.Running,
				LastSync: p.LastSync,
			},
		}
	}

	writeJSON(w, schemas.StatusResponse{
		Profiles: profiles,
		Scheduler: map[string]any{
			"running": st.Running,
		},
	})
}

func (d Deps) syncAll(w http.ResponseWriter, r *http.Request) {
	go d.Scheduler.Trigger(context.Background(), "", nil)
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) syncProfile(w http.ResponseWriter, r *http.Request) {
	profile := chi.URLParam(r, "profile")
	go d.Scheduler.Trigger(context.Background(), profile, nil)
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) syncOk(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) listMappings(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > 250 {
		perPage = 25
	}
	items, total, err := animap.List(r.Context(), d.DB, page, perPage, r.URL.Query().Get("q"), r.URL.Query().Get("custom_only") == "true")
	if err != nil {
		writeErr(w, err, 500)
		return
	}
	withAniList := r.URL.Query().Get("with_anilist") == "true"
	anilistByID := map[string]any{}
	if withAniList {
		anilistByID = fetchAniListMetadata(r.Context(), anilistIDsFromMappings(items))
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		edges := make([]map[string]any, 0, len(item.Edges))
		var anilist any
		if item.Provider == "anilist" {
			anilist = anilistByID[item.EntryID]
		}
		for _, edge := range item.Edges {
			edges = append(edges, map[string]any{
				"target_provider":   edge.TargetProvider,
				"target_entry_id":   edge.TargetEntryID,
				"target_scope":      nullString(edge.TargetScope),
				"source_range":      edge.SourceRange,
				"destination_range": nullString(edge.DestRange),
				"sources":           edge.Sources,
			})
			if anilist == nil && edge.TargetProvider == "anilist" {
				anilist = anilistByID[edge.TargetEntryID]
			}
		}
		out = append(out, map[string]any{
			"descriptor": item.Descriptor,
			"provider":   item.Provider,
			"entry_id":   item.EntryID,
			"scope":      nullString(item.Scope),
			"edges":      edges,
			"custom":     item.Custom,
			"sources":    item.Sources,
			"anilist":    anilist,
		})
	}
	pages := 1
	if perPage > 0 {
		pages = (total + perPage - 1) / perPage
	}
	writeJSON(w, map[string]any{
		"items":        out,
		"total":        total,
		"page":         page,
		"per_page":     perPage,
		"pages":        pages,
		"with_anilist": withAniList,
	})
}

func (d Deps) mappingQueryCapabilities(w http.ResponseWriter, r *http.Request) {
	providers := []string{}
	rows, err := d.DB.QueryContext(r.Context(), "SELECT DISTINCT provider FROM animap_entry WHERE provider != '' ORDER BY provider")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var provider string
			if scanErr := rows.Scan(&provider); scanErr == nil && strings.TrimSpace(provider) != "" {
				providers = append(providers, provider)
			}
		}
	}

	stringOps := []string{"=", "*", "?", "in"}
	intOps := []string{"=", ">", ">=", "<", "<=", "range", "in"}
	enumOps := []string{"=", "in"}
	fields := []map[string]any{
		{"key": "source.descriptor", "aliases": []string{}, "type": "string", "operators": stringOps, "desc": "Source descriptor (provider:id[:scope])"},
		{"key": "source.provider", "aliases": []string{}, "type": "string", "operators": stringOps, "values": providers, "desc": "Source provider"},
		{"key": "source.id", "aliases": []string{}, "type": "string", "operators": stringOps, "desc": "Source entry identifier"},
		{"key": "source.scope", "aliases": []string{}, "type": "string", "operators": []string{"="}, "desc": "Source entry scope"},
		{"key": "target.descriptor", "aliases": []string{}, "type": "string", "operators": stringOps, "desc": "Destination descriptor (provider:id[:scope])"},
		{"key": "target.provider", "aliases": []string{}, "type": "string", "operators": stringOps, "values": providers, "desc": "Destination provider"},
		{"key": "target.id", "aliases": []string{}, "type": "string", "operators": stringOps, "desc": "Destination entry identifier"},
		{"key": "target.scope", "aliases": []string{}, "type": "string", "operators": []string{"="}, "desc": "Destination entry scope"},
		{"key": "edge.source_range", "aliases": []string{}, "type": "string", "operators": stringOps, "desc": "Source episode range"},
		{"key": "edge.target_range", "aliases": []string{}, "type": "string", "operators": stringOps, "desc": "Destination episode range"},
		{"key": "anilist.title", "aliases": []string{}, "type": "string", "operators": []string{"="}, "desc": "AniList title search"},
		{"key": "anilist.id", "aliases": []string{}, "type": "int", "operators": intOps, "desc": "AniList ID"},
		{"key": "anilist.duration", "aliases": []string{}, "type": "int", "operators": intOps, "desc": "Episode duration"},
		{"key": "anilist.episodes", "aliases": []string{}, "type": "int", "operators": intOps, "desc": "Episode count"},
		{"key": "anilist.start_date", "aliases": []string{}, "type": "int", "operators": intOps, "desc": "Start date (YYYYMMDD)"},
		{"key": "anilist.end_date", "aliases": []string{}, "type": "int", "operators": intOps, "desc": "End date (YYYYMMDD)"},
		{"key": "anilist.format", "aliases": []string{}, "type": "enum", "operators": enumOps, "values": []string{"TV", "TV_SHORT", "MOVIE", "SPECIAL", "OVA", "ONA", "MUSIC"}, "desc": "AniList format"},
		{"key": "anilist.status", "aliases": []string{}, "type": "enum", "operators": enumOps, "values": []string{"FINISHED", "RELEASING", "NOT_YET_RELEASED", "CANCELLED"}, "desc": "AniList status"},
		{"key": "anilist.average_score", "aliases": []string{}, "type": "int", "operators": intOps, "desc": "Average score"},
		{"key": "anilist.popularity", "aliases": []string{}, "type": "int", "operators": intOps, "desc": "Popularity score"},
		{"key": "anilist.genre", "aliases": []string{}, "type": "string", "operators": enumOps, "desc": "AniList genre"},
		{"key": "anilist.tag", "aliases": []string{}, "type": "string", "operators": enumOps, "desc": "AniList tag"},
	}
	writeJSON(w, map[string]any{"fields": fields})
}

func (d Deps) getMapping(w http.ResponseWriter, r *http.Request) {
	descriptor := chi.URLParam(r, "descriptor")
	parsed, err := animap.ParseDescriptor(descriptor)
	if err != nil {
		writeErr(w, err, 400)
		return
	}
	detail, err := animap.GetDetail(r.Context(), d.DB, parsed)
	if err != nil {
		writeErr(w, err, 500)
		return
	}
	writeJSON(w, mappingDetailJSON(detail))
}

func (d Deps) upsertMapping(w http.ResponseWriter, r *http.Request) {
	var m struct {
		Descriptor string `json:"descriptor"`
		Targets    []struct {
			Provider string  `json:"provider"`
			EntryID  string  `json:"entry_id"`
			Scope    *string `json:"scope"`
			Ranges   []struct {
				SourceRange string  `json:"source_range"`
				DestRange   *string `json:"destination_range"`
			} `json:"ranges"`
			Deleted bool `json:"deleted"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeErr(w, err, 400)
		return
	}
	source, err := animap.ParseDescriptor(m.Descriptor)
	if err != nil {
		writeErr(w, err, 400)
		return
	}
	for _, t := range m.Targets {
		if t.Deleted {
			continue
		}
		target := animap.Descriptor{Provider: strings.TrimSpace(t.Provider), EntryID: strings.TrimSpace(t.EntryID)}
		if t.Scope != nil && strings.TrimSpace(*t.Scope) != "" {
			target.Scope = sql.NullString{String: strings.TrimSpace(*t.Scope), Valid: true}
		}
		ranges := map[string]sql.NullString{}
		for _, rng := range t.Ranges {
			dst := sql.NullString{}
			if rng.DestRange != nil {
				dst = sql.NullString{String: *rng.DestRange, Valid: true}
			}
			if strings.TrimSpace(rng.SourceRange) != "" {
				ranges[strings.TrimSpace(rng.SourceRange)] = dst
			}
		}
		if len(ranges) == 0 {
			ranges["1"] = sql.NullString{}
		}
		if err := animap.UpsertCustom(r.Context(), d.DB, source, target, ranges); err != nil {
			writeErr(w, err, 500)
			return
		}
	}
	detail, err := animap.GetDetail(r.Context(), d.DB, source)
	if err != nil {
		writeErr(w, err, 500)
		return
	}
	writeJSON(w, mappingDetailJSON(detail))
}

func (d Deps) deleteMapping(w http.ResponseWriter, r *http.Request) {
	descriptor := chi.URLParam(r, "descriptor")
	parsed, err := animap.ParseDescriptor(descriptor)
	if err != nil {
		writeErr(w, err, 400)
		return
	}
	if err := animap.DeleteCustom(r.Context(), d.DB, parsed); err != nil {
		writeErr(w, err, 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) syncDatabase(w http.ResponseWriter, r *http.Request) {
	client := animap.NewClient(d.Config.DataDir, d.Config.MappingsURL)
	if err := client.SyncDB(r.Context(), d.DB); err != nil {
		writeErr(w, err, 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) listPins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": []any{}})
}

func (d Deps) upsertPin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) deletePin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) historyAll(w http.ResponseWriter, r *http.Request) {
	d.historyQuery(w, r, "")
}

func (d Deps) historyProfile(w http.ResponseWriter, r *http.Request) {
	d.historyQuery(w, r, chi.URLParam(r, "profile"))
}

func (d Deps) historySingle(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"item": map[string]any{}})
}

func (d Deps) historyQuery(w http.ResponseWriter, r *http.Request, profile string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	builder := sq.Select("id", "profile", "provider", "item_id", "action", "status", "message", "dry_run", "created_at").From("sync_history").OrderBy("id DESC").Limit(uint64(limit))
	if profile != "" {
		builder = builder.Where(sq.Eq{"profile": profile})
	}

	rows, err := builder.RunWith(d.DB).QueryContext(r.Context())
	if err != nil {
		writeErr(w, err, 500)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var prof, provider, itemID, action, status, message, created string
		var dry bool
		if err := rows.Scan(&id, &prof, &provider, &itemID, &action, &status, &message, &dry, &created); err != nil {
			writeErr(w, err, 500)
			return
		}
		items = append(items, map[string]any{"id": id, "profile": prof, "provider": provider, "item_id": itemID, "action": action, "status": status, "message": message, "dry_run": dry, "created_at": created})
	}
	writeJSON(w, map[string]any{"items": items, "total": len(items)})
}

func (d Deps) logsFiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, []map[string]any{
		{"name": "anibridge-go.log", "size": 1024, "mtime": time.Now().UnixMilli(), "current": true},
	})
}

func (d Deps) logsFile(w http.ResponseWriter, r *http.Request) {
	entries := []map[string]any{}
	for _, l := range d.Logs.List() {
		entries = append(entries, map[string]any{
			"timestamp": time.Now().Format("2006-01-02 15:04:05"),
			"level":     l.Level,
			"message":   l.Message,
		})
	}
	writeJSON(w, entries)
}

func (d Deps) systemAbout(w http.ResponseWriter, r *http.Request) {
	info := map[string]any{
		"version":        utils.Version,
		"git_hash":       "unknown",
		"python":         "Go " + runtime.Version(),
		"platform":       runtime.GOOS,
		"utc_now":        time.Now().UTC().Format(time.RFC3339),
		"uptime_seconds": int(time.Since(d.StartedAt).Seconds()),
	}
	writeJSON(w, map[string]any{
		"info":      info,
		"process":   map[string]any{"pid": 1, "memory_mb": utils.RSSBytes() / 1024 / 1024},
		"scheduler": map[string]any{"running": true},
		"status":    map[string]any{},
	})
}

func (d Deps) systemMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"version":  utils.Version,
		"git_hash": "unknown",
	})
}

func anilistIDsFromMappings(items []animap.Item) []int {
	seen := map[int]bool{}
	ids := []int{}
	add := func(raw string) {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, item := range items {
		if item.Provider == "anilist" {
			add(item.EntryID)
		}
		for _, edge := range item.Edges {
			if edge.TargetProvider == "anilist" {
				add(edge.TargetEntryID)
			}
		}
	}
	return ids
}

func fetchAniListMetadata(ctx context.Context, ids []int) map[string]any {
	out := map[string]any{}
	if len(ids) == 0 {
		return out
	}

	query := `query BatchGetAnime($ids: [Int]) {
		Page(perPage: 50) {
			media(id_in: $ids, type: ANIME) {
				id
				format
				status
				season
				seasonYear
				episodes
				duration
				isAdult
				coverImage { medium }
				title { romaji english native userPreferred }
			}
		}
	}`

	for start := 0; start < len(ids); start += 50 {
		end := start + 50
		if end > len(ids) {
			end = len(ids)
		}
		payload, err := json.Marshal(map[string]any{"query": query, "variables": map[string]any{"ids": ids[start:end]}})
		if err != nil {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://graphql.anilist.co", bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "AniBridge GO")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		var decoded struct {
			Data struct {
				Page struct {
					Media []map[string]any `json:"media"`
				} `json:"Page"`
			} `json:"data"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&decoded)
		resp.Body.Close()
		if decodeErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		for _, media := range decoded.Data.Page.Media {
			if rawID, ok := media["id"].(float64); ok {
				out[strconv.Itoa(int(rawID))] = media
			}
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func nullString(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func mappingDetailJSON(detail animap.Detail) map[string]any {
	targets := make([]map[string]any, 0, len(detail.Targets))
	effective := map[string]map[string]any{}
	for _, target := range detail.Targets {
		ranges := make([]map[string]any, 0, len(target.Ranges))
		targetRanges := map[string]any{}
		for _, rng := range target.Ranges {
			ranges = append(ranges, map[string]any{
				"source_range": rng.SourceRange,
				"upstream":     nullString(rng.Upstream),
				"custom":       nullString(rng.Custom),
				"effective":    nullString(rng.Effective),
				"origin":       rng.Origin,
				"inherited":    rng.Inherited,
			})
			targetRanges[rng.SourceRange] = nullString(rng.Effective)
		}
		effective[target.Descriptor] = targetRanges
		targets = append(targets, map[string]any{
			"descriptor": target.Descriptor,
			"provider":   target.Provider,
			"entry_id":   target.EntryID,
			"scope":      nullString(target.Scope),
			"origin":     target.Origin,
			"deleted":    target.Deleted,
			"ranges":     ranges,
		})
	}
	return map[string]any{
		"descriptor": detail.Descriptor,
		"provider":   detail.Provider,
		"entry_id":   detail.EntryID,
		"scope":      nullString(detail.Scope),
		"layers": map[string]any{
			"upstream":  map[string]any{},
			"custom":    map[string]any{},
			"effective": effective,
		},
		"targets": targets,
	}
}

func writeErr(w http.ResponseWriter, err error, code int) {
	w.WriteHeader(code)
	writeJSON(w, schemas.APIError{Error: err.Error()})
}
