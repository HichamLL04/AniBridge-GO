package animap

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/klauspost/compress/zstd"
	"gopkg.in/yaml.v3"
)

type Descriptor struct {
	Provider string
	EntryID  string
	Scope    sql.NullString
}

type Edge struct {
	TargetProvider string
	TargetEntryID  string
	TargetScope    sql.NullString
	SourceRange    string
	DestRange      sql.NullString
	Sources        []string
}

type Item struct {
	Descriptor string
	Provider   string
	EntryID    string
	Scope      sql.NullString
	Edges      []Edge
	Custom     bool
	Sources    []string
}

type DetailTarget struct {
	Descriptor string
	Provider   string
	EntryID    string
	Scope      sql.NullString
	Origin     string
	Deleted    bool
	Ranges     []DetailRange
}

type DetailRange struct {
	SourceRange string
	Upstream    sql.NullString
	Custom      sql.NullString
	Effective   sql.NullString
	Origin      string
	Inherited   bool
}

type Detail struct {
	Descriptor string
	Provider   string
	EntryID    string
	Scope      sql.NullString
	Targets    []DetailTarget
}

type Client struct {
	DataDir     string
	UpstreamURL string

	loaded     map[string]bool
	provenance map[string][]string
}

type rawMappings map[string]any

var mappingFiles = []string{"mappings.yaml", "mappings.yml", "mappings.json", "mappings.yaml.zst", "mappings.yml.zst", "mappings.json.zst"}

func ParseDescriptor(raw string) (Descriptor, error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return Descriptor{}, fmt.Errorf("invalid mapping descriptor %q", raw)
	}
	d := Descriptor{Provider: strings.TrimSpace(parts[0]), EntryID: strings.TrimSpace(parts[1])}
	if d.Provider == "" || d.EntryID == "" {
		return Descriptor{}, fmt.Errorf("invalid mapping descriptor %q", raw)
	}
	if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
		d.Scope = sql.NullString{String: strings.TrimSpace(parts[2]), Valid: true}
	}
	return d, nil
}

func DescriptorKey(d Descriptor) string {
	if d.Scope.Valid && d.Scope.String != "" {
		return d.Provider + ":" + d.EntryID + ":" + d.Scope.String
	}
	return d.Provider + ":" + d.EntryID
}

func NewClient(dataDir, upstreamURL string) *Client {
	return &Client{DataDir: dataDir, UpstreamURL: upstreamURL}
}

func (c *Client) Load(ctx context.Context) (rawMappings, map[string][]string, error) {
	c.loaded = map[string]bool{}
	c.provenance = map[string][]string{}

	merged := rawMappings{}
	if strings.TrimSpace(c.UpstreamURL) != "" {
		upstream, err := c.loadSource(ctx, c.UpstreamURL, nil)
		if err != nil {
			return nil, nil, err
		}
		deepMerge(merged, upstream)
	}

	if custom := c.customPath(); custom != "" {
		local, err := c.loadSource(ctx, custom, nil)
		if err != nil {
			return nil, nil, err
		}
		deepMerge(merged, local)
	}

	for key := range merged {
		if strings.HasPrefix(key, "$") {
			delete(merged, key)
		}
	}
	return merged, c.provenance, nil
}

func (c *Client) SyncDB(ctx context.Context, db *sql.DB) error {
	mappings, provenance, err := c.Load(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM animap_provenance"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM animap_mapping WHERE custom = 0"); err != nil {
		return err
	}

	keys := make([]string, 0, len(mappings))
	for key := range mappings {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, sourceKey := range keys {
		source, err := ParseDescriptor(sourceKey)
		if err != nil {
			continue
		}
		targets, ok := asMap(mappings[sourceKey])
		if !ok {
			continue
		}
		sourceID, err := ensureEntry(ctx, tx, source)
		if err != nil {
			return err
		}
		for targetKey, rawRanges := range targets {
			if strings.HasPrefix(targetKey, "$") || rawRanges == nil {
				continue
			}
			target, err := ParseDescriptor(targetKey)
			if err != nil {
				continue
			}
			ranges, ok := asMap(rawRanges)
			if !ok {
				continue
			}
			destID, err := ensureEntry(ctx, tx, target)
			if err != nil {
				return err
			}
			for srcRange, dstAny := range ranges {
				dst := sql.NullString{}
				if dstAny != nil {
					dst.Valid = true
					dst.String = fmt.Sprint(dstAny)
				}
				mappingID, err := insertMapping(ctx, tx, sourceID, destID, srcRange, dst, false)
				if err != nil {
					return err
				}
				for i, src := range provenance[sourceKey] {
					if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO animap_provenance(mapping_id, n, source) VALUES(?, ?, ?)", mappingID, i, src); err != nil {
						return err
					}
				}
			}
		}
	}
	return tx.Commit()
}

func (c *Client) customPath() string {
	for _, name := range mappingFiles {
		path := filepath.Join(c.DataDir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (c *Client) loadSource(ctx context.Context, src string, chain map[string]bool) (rawMappings, error) {
	if chain == nil {
		chain = map[string]bool{}
	}
	if chain[src] || c.loaded[src] {
		return rawMappings{}, nil
	}
	chain[src] = true
	c.loaded[src] = true

	payload, err := readSource(ctx, src)
	if err != nil {
		return nil, err
	}
	data, err := decode(payload, src)
	if err != nil {
		return nil, err
	}

	merged := rawMappings{}
	includes, _ := data["$includes"].([]any)
	for _, inc := range includes {
		resolved := resolveInclude(fmt.Sprint(inc), src)
		included, err := c.loadSource(ctx, resolved, cloneBoolMap(chain))
		if err != nil {
			return nil, err
		}
		deepMerge(merged, included)
	}
	deepMerge(merged, data)

	for key := range merged {
		if !strings.HasPrefix(key, "$") {
			c.provenance[key] = appendUnique(c.provenance[key], src)
		}
	}
	return merged, nil
}

func readSource(ctx context.Context, src string) ([]byte, error) {
	if u, err := url.Parse(src); err == nil && u.Scheme != "" && u.Host != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json, application/yaml, text/yaml")
		req.Header.Set("User-Agent", "AniBridge GO")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch mappings %s: %s", src, resp.Status)
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(src)
}

func decode(payload []byte, src string) (rawMappings, error) {
	var out map[string]any
	ext := strings.ToLower(filepath.Ext(src))
	if ext == ".zst" {
		decoder, err := zstd.NewReader(nil)
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		payload, err = decoder.DecodeAll(payload, nil)
		if err != nil {
			return nil, err
		}
		src = strings.TrimSuffix(src, filepath.Ext(src))
		ext = strings.ToLower(filepath.Ext(src))
	}
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(payload, &out); err != nil {
			return nil, err
		}
	default:
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.UseNumber()
		if err := dec.Decode(&out); err != nil {
			return nil, err
		}
	}
	if out == nil {
		return rawMappings{}, nil
	}
	return normalizeMap(out), nil
}

func normalizeMap(in map[string]any) rawMappings {
	out := rawMappings{}
	for k, v := range in {
		switch typed := v.(type) {
		case map[string]any:
			out[k] = normalizeMap(typed)
		case map[any]any:
			next := map[string]any{}
			for mk, mv := range typed {
				next[fmt.Sprint(mk)] = mv
			}
			out[k] = normalizeMap(next)
		case json.Number:
			out[k] = typed.String()
		default:
			out[k] = typed
		}
	}
	return out
}

func resolveInclude(include, parent string) string {
	if u, err := url.Parse(include); err == nil && u.Scheme != "" && u.Host != "" {
		return include
	}
	if u, err := url.Parse(parent); err == nil && u.Scheme != "" && u.Host != "" {
		return u.ResolveReference(&url.URL{Path: include}).String()
	}
	if filepath.IsAbs(include) {
		return include
	}
	return filepath.Join(filepath.Dir(parent), include)
}

func deepMerge(base, override rawMappings) {
	for key, value := range override {
		if b, ok := asMap(base[key]); ok {
			if o, ok := asMap(value); ok {
				deepMerge(b, o)
				base[key] = b
				continue
			}
		}
		base[key] = value
	}
}

func asMap(value any) (rawMappings, bool) {
	switch typed := value.(type) {
	case rawMappings:
		return typed, true
	case map[string]any:
		return normalizeMap(typed), true
	default:
		return nil, false
	}
}

func ensureEntry(ctx context.Context, tx *sql.Tx, d Descriptor) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM animap_entry WHERE provider = ? AND entry_id = ? AND (entry_scope IS ? OR entry_scope = ?)", d.Provider, d.EntryID, nullArg(d.Scope), nullArg(d.Scope)).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO animap_entry(provider, entry_id, entry_scope) VALUES(?, ?, ?)", d.Provider, d.EntryID, nullArg(d.Scope))
	if err != nil {
		return 0, err
	}
	err = tx.QueryRowContext(ctx, "SELECT id FROM animap_entry WHERE provider = ? AND entry_id = ? AND (entry_scope IS ? OR entry_scope = ?)", d.Provider, d.EntryID, nullArg(d.Scope), nullArg(d.Scope)).Scan(&id)
	return id, err
}

func insertMapping(ctx context.Context, tx *sql.Tx, sourceID, destID int64, sourceRange string, destRange sql.NullString, custom bool) (int64, error) {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO animap_mapping(source_entry_id, destination_entry_id, source_range, destination_range, custom, updated_at) VALUES(?, ?, ?, ?, ?, ?)`, sourceID, destID, sourceRange, nullArg(destRange), custom, time.Now())
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM animap_mapping WHERE source_entry_id = ? AND destination_entry_id = ? AND source_range = ? AND (destination_range IS ? OR destination_range = ?)`, sourceID, destID, sourceRange, nullArg(destRange), nullArg(destRange)).Scan(&id)
	return id, err
}

func UpsertCustom(ctx context.Context, db *sql.DB, source Descriptor, target Descriptor, ranges map[string]sql.NullString) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sourceID, err := ensureEntry(ctx, tx, source)
	if err != nil {
		return err
	}
	destID, err := ensureEntry(ctx, tx, target)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM animap_mapping WHERE custom = 1 AND source_entry_id = ? AND destination_entry_id = ?", sourceID, destID); err != nil {
		return err
	}
	for srcRange, dstRange := range ranges {
		if _, err := insertMapping(ctx, tx, sourceID, destID, srcRange, dstRange, true); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func DeleteCustom(ctx context.Context, db *sql.DB, source Descriptor) error {
	_, err := db.ExecContext(ctx, `DELETE FROM animap_mapping WHERE custom = 1 AND source_entry_id IN (
		SELECT id FROM animap_entry WHERE provider = ? AND entry_id = ? AND (entry_scope IS ? OR entry_scope = ?)
	)`, source.Provider, source.EntryID, nullArg(source.Scope), nullArg(source.Scope))
	return err
}

func List(ctx context.Context, db *sql.DB, page, perPage int, query string, customOnly bool) ([]Item, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 25
	}
	filter := sq.And{}
	if customOnly {
		filter = append(filter, sq.Eq{"m.custom": true})
	}
	if strings.TrimSpace(query) != "" {
		like := "%" + strings.TrimSpace(query) + "%"
		filter = append(filter, sq.Or{sq.Like{"s.provider": like}, sq.Like{"s.entry_id": like}, sq.Like{"d.provider": like}, sq.Like{"d.entry_id": like}})
	}

	countBuilder := sq.Select("COUNT(DISTINCT s.id)").From("animap_entry s").Join("animap_mapping m ON m.source_entry_id = s.id").Join("animap_entry d ON d.id = m.destination_entry_id").Where(filter).RunWith(db)
	var total int
	if err := countBuilder.QueryRowContext(ctx).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := sq.Select("DISTINCT s.id", "s.provider", "s.entry_id", "s.entry_scope").
		From("animap_entry s").Join("animap_mapping m ON m.source_entry_id = s.id").Join("animap_entry d ON d.id = m.destination_entry_id").
		Where(filter).OrderBy("s.provider ASC", "s.entry_id ASC").Limit(uint64(perPage)).Offset(uint64((page - 1) * perPage)).
		RunWith(db).QueryContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	type sourceRow struct {
		id   int64
		item Item
	}
	sourceRows := []sourceRow{}
	for rows.Next() {
		var sourceID int64
		var item Item
		if err := rows.Scan(&sourceID, &item.Provider, &item.EntryID, &item.Scope); err != nil {
			rows.Close()
			return nil, 0, err
		}
		item.Descriptor = DescriptorKey(Descriptor{Provider: item.Provider, EntryID: item.EntryID, Scope: item.Scope})
		sourceRows = append(sourceRows, sourceRow{id: sourceID, item: item})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()

	items := make([]Item, 0, len(sourceRows))
	for _, row := range sourceRows {
		edges, custom, sources, err := edgesForSource(ctx, db, row.id)
		if err != nil {
			return nil, 0, err
		}
		row.item.Edges = edges
		row.item.Custom = custom
		row.item.Sources = sources
		items = append(items, row.item)
	}
	return items, total, nil
}

func GetDetail(ctx context.Context, db *sql.DB, source Descriptor) (Detail, error) {
	var sourceID int64
	err := db.QueryRowContext(ctx, "SELECT id FROM animap_entry WHERE provider = ? AND entry_id = ? AND (entry_scope IS ? OR entry_scope = ?)", source.Provider, source.EntryID, nullArg(source.Scope), nullArg(source.Scope)).Scan(&sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return Detail{Descriptor: DescriptorKey(source), Provider: source.Provider, EntryID: source.EntryID, Scope: source.Scope}, nil
	}
	if err != nil {
		return Detail{}, err
	}
	edges, _, _, err := edgesForSource(ctx, db, sourceID)
	if err != nil {
		return Detail{}, err
	}
	targets := make(map[string]*DetailTarget)
	for _, edge := range edges {
		targetDesc := Descriptor{Provider: edge.TargetProvider, EntryID: edge.TargetEntryID, Scope: edge.TargetScope}
		key := DescriptorKey(targetDesc)
		t := targets[key]
		if t == nil {
			t = &DetailTarget{Descriptor: key, Provider: edge.TargetProvider, EntryID: edge.TargetEntryID, Scope: edge.TargetScope, Origin: "upstream"}
			targets[key] = t
		}
		origin := "upstream"
		if contains(edge.Sources, "custom") || contains(edge.Sources, "user") {
			origin = "custom"
			t.Origin = "custom"
		}
		t.Ranges = append(t.Ranges, DetailRange{SourceRange: edge.SourceRange, Effective: edge.DestRange, Origin: origin})
	}
	detail := Detail{Descriptor: DescriptorKey(source), Provider: source.Provider, EntryID: source.EntryID, Scope: source.Scope}
	for _, key := range sortedTargetKeys(targets) {
		detail.Targets = append(detail.Targets, *targets[key])
	}
	return detail, nil
}

func ResolveAniList(ctx context.Context, db *sql.DB, external map[string]string) (int64, error) {
	for provider, id := range external {
		if provider == "anilist" {
			parsed, err := strconv.ParseInt(id, 10, 64)
			if err == nil && parsed > 0 {
				return parsed, nil
			}
			continue
		}
		var out string
		err := db.QueryRowContext(ctx, `SELECT d.entry_id
			FROM animap_entry s
			JOIN animap_mapping m ON m.source_entry_id = s.id
			JOIN animap_entry d ON d.id = m.destination_entry_id
			WHERE s.provider = ? AND s.entry_id = ? AND d.provider = 'anilist'
			ORDER BY m.custom DESC, m.id ASC LIMIT 1`, provider, id).Scan(&out)
		if err == nil {
			return strconv.ParseInt(out, 10, 64)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	return 0, nil
}

func edgesForSource(ctx context.Context, db *sql.DB, sourceID int64) ([]Edge, bool, []string, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.id, d.provider, d.entry_id, d.entry_scope, m.source_range, m.destination_range, m.custom
		FROM animap_mapping m
		JOIN animap_entry d ON d.id = m.destination_entry_id
		WHERE m.source_entry_id = ?
		ORDER BY d.provider, d.entry_id, m.source_range`, sourceID)
	if err != nil {
		return nil, false, nil, err
	}
	type rowData struct {
		mappingID int64
		edge      Edge
		custom    bool
	}
	var rowsData []rowData
	custom := false
	for rows.Next() {
		var isCustom bool
		var edge Edge
		var mappingID int64
		if err := rows.Scan(&mappingID, &edge.TargetProvider, &edge.TargetEntryID, &edge.TargetScope, &edge.SourceRange, &edge.DestRange, &isCustom); err != nil {
			rows.Close()
			return nil, false, nil, err
		}
		if isCustom {
			custom = true
		}
		rowsData = append(rowsData, rowData{mappingID: mappingID, edge: edge, custom: isCustom})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, nil, err
	}
	rows.Close()

	var edges []Edge
	allSources := []string{}
	for _, row := range rowsData {
		edge := row.edge
		if row.custom {
			edge.Sources = []string{"custom"}
		} else {
			edge.Sources, err = provenance(ctx, db, row.mappingID)
			if err != nil {
				return nil, false, nil, err
			}
			if len(edge.Sources) == 0 {
				edge.Sources = []string{"upstream"}
			}
		}
		allSources = appendUniqueMany(allSources, edge.Sources)
		edges = append(edges, edge)
	}
	return edges, custom, allSources, nil
}

func provenance(ctx context.Context, db *sql.DB, mappingID int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT source FROM animap_provenance WHERE mapping_id = ? ORDER BY n", mappingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func nullArg(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func appendUniqueMany(items []string, values []string) []string {
	for _, value := range values {
		items = appendUnique(items, value)
	}
	return items
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func sortedTargetKeys(targets map[string]*DetailTarget) []string {
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
