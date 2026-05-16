package services

import (
	"database/sql"
)

const initMigration = `
CREATE TABLE IF NOT EXISTS animap (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  provider TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  anilist_id INTEGER NOT NULL,
  title TEXT NOT NULL,
  titles_json TEXT NOT NULL DEFAULT '[]',
  user_override INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(provider, provider_id)
);

CREATE TABLE IF NOT EXISTS animap_entry (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  provider TEXT NOT NULL,
  entry_id TEXT NOT NULL,
  entry_scope TEXT,
  UNIQUE(provider, entry_id, entry_scope)
);

CREATE INDEX IF NOT EXISTS ix_animap_entry_provider_entry_id ON animap_entry(provider, entry_id);
CREATE INDEX IF NOT EXISTS ix_animap_entry_scope ON animap_entry(entry_scope);

CREATE TABLE IF NOT EXISTS animap_mapping (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_entry_id INTEGER NOT NULL REFERENCES animap_entry(id) ON DELETE CASCADE,
  destination_entry_id INTEGER NOT NULL REFERENCES animap_entry(id) ON DELETE CASCADE,
  source_range TEXT NOT NULL,
  destination_range TEXT,
  custom INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(source_entry_id, destination_entry_id, source_range, destination_range)
);

CREATE INDEX IF NOT EXISTS ix_animap_mapping_source ON animap_mapping(source_entry_id);
CREATE INDEX IF NOT EXISTS ix_animap_mapping_destination_source ON animap_mapping(destination_entry_id, source_entry_id);
CREATE INDEX IF NOT EXISTS ix_animap_mapping_custom ON animap_mapping(custom);

CREATE TABLE IF NOT EXISTS animap_provenance (
  mapping_id INTEGER NOT NULL REFERENCES animap_mapping(id) ON DELETE CASCADE,
  n INTEGER NOT NULL,
  source TEXT NOT NULL,
  PRIMARY KEY(mapping_id, n)
);

CREATE TABLE IF NOT EXISTS sync_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  profile TEXT NOT NULL,
  provider TEXT NOT NULL,
  item_id TEXT NOT NULL,
  action TEXT NOT NULL,
  status TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  dry_run INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS pin (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  title TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(kind, ref)
);

CREATE TABLE IF NOT EXISTS housekeeping (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

func ApplyEmbeddedMigrations(db *sql.DB) error { _, err := db.Exec(initMigration); return err }
