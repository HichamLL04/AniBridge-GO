package animap

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

const testSchema = `
CREATE TABLE animap_entry (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  provider TEXT NOT NULL,
  entry_id TEXT NOT NULL,
  entry_scope TEXT,
  UNIQUE(provider, entry_id, entry_scope)
);
CREATE TABLE animap_mapping (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_entry_id INTEGER NOT NULL,
  destination_entry_id INTEGER NOT NULL,
  source_range TEXT NOT NULL,
  destination_range TEXT,
  custom INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(source_entry_id, destination_entry_id, source_range, destination_range)
);
CREATE TABLE animap_provenance (
  mapping_id INTEGER NOT NULL,
  n INTEGER NOT NULL,
  source TEXT NOT NULL,
  PRIMARY KEY(mapping_id, n)
);`

func TestClientSyncDBAndResolveAniList(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mappings.json"), []byte(`{
		"tmdb:10": {"anilist:20": {"1": null}},
		"tvdb:30:series": {"anilist:40": {"1-12": "1-12"}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, testSchema); err != nil {
		t.Fatal(err)
	}

	client := NewClient(dir, "")
	if err := client.SyncDB(ctx, db); err != nil {
		t.Fatal(err)
	}

	id, err := ResolveAniList(ctx, db, map[string]string{"tmdb": "10"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 20 {
		t.Fatalf("ResolveAniList() = %d, want 20", id)
	}

	items, total, err := List(ctx, db, 1, 10, "tvdb", false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].Descriptor != "tvdb:30:series" {
		t.Fatalf("List() total=%d items=%v", total, items)
	}
}

func TestClientLoadsZstdMappings(t *testing.T) {
	dir := t.TempDir()
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := encoder.EncodeAll([]byte(`{"tmdb:1":{"anilist:2":{"1":null}}}`), nil)
	if err := os.WriteFile(filepath.Join(dir, "mappings.json.zst"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	client := NewClient(dir, "")
	mappings, _, err := client.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mappings["tmdb:1"]; !ok {
		t.Fatalf("zstd mappings were not loaded: %v", mappings)
	}
}
