package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	sq "github.com/Masterminds/squirrel"

	 "anibridge-go/internal/config"
	 "anibridge-go/internal/core/animap"
	 "anibridge-go/internal/core/providers"
	 "anibridge-go/internal/web/services"
)

type Engine struct {
	cfg config.Config
	db  *sql.DB
	hub *services.Hub
}

func NewEngine(cfg config.Config, db *sql.DB, hub *services.Hub) *Engine {
	return &Engine{cfg: cfg, db: db, hub: hub}
}

func (e *Engine) RunProfile(ctx context.Context, profile config.ProfileConfig, dryRun bool) error {
	slog.Info("sync profile started", "profile", profile.Name, "dry_run", dryRun)

	var libClass, listClass *config.ProviderClass
	for i, c := range e.cfg.ProviderClasses {
		if c.Namespace == profile.LibraryProvider {
			libClass = &e.cfg.ProviderClasses[i]
		}
		if c.Namespace == profile.ListProvider {
			listClass = &e.cfg.ProviderClasses[i]
		}
	}

	if libClass == nil {
		return fmt.Errorf("library provider %q not found in config", profile.LibraryProvider)
	}
	if listClass == nil {
		return fmt.Errorf("list provider %q not found in config", profile.ListProvider)
	}

	// Build and initialize providers
	libInst, err := providers.Build("library", libClass.Namespace, libClass.Settings)
	if err != nil {
		return fmt.Errorf("build library provider: %w", err)
	}
	libProv := libInst.(providers.LibraryProvider)
	if err := libProv.Initialize(ctx); err != nil {
		return fmt.Errorf("init library provider: %w", err)
	}

	listInst, err := providers.Build("list", listClass.Namespace, listClass.Settings)
	if err != nil {
		return fmt.Errorf("build list provider: %w", err)
	}
	listProv := listInst.(providers.ListProvider)
	if err := listProv.Initialize(ctx); err != nil {
		return fmt.Errorf("init list provider: %w", err)
	}

	// Convert profile fields to SyncFields
	var fields []providers.SyncField
	for _, f := range profile.Fields {
		fields = append(fields, providers.SyncField(f))
	}

	// Scan library
	items, err := libProv.Scan(ctx)
	if err != nil {
		return fmt.Errorf("scan library: %w", err)
	}
	slog.Info("sync profile scanned items", "profile", profile.Name, "count", len(items))

	var updates, skips int
	for _, item := range items {
		// Map to AniList ID
		aniListID, err := e.getAniListID(ctx, item)
		if err != nil {
			slog.Warn("sync error mapping item", "title", item.Title, "error", err)
			continue
		}
		if aniListID == 0 {
			skips++
			continue // No mapping found
		}

		// Get current list entry
		entry, err := listProv.GetEntry(ctx, aniListID)
		if err != nil {
			slog.Error("sync error getting entry", "anilist_id", aniListID, "error", err)
			continue
		}

		// Determine if update is needed
		var needsUpdate bool
		updateEntry := providers.ListEntry{AniListID: aniListID}
		if entry == nil {
			// Not on list, add it
			needsUpdate = true
			updateEntry.Status = item.Status
			updateEntry.Progress = item.Progress
		} else {
			// Compare fields
			for _, f := range fields {
				switch f {
				case providers.FieldStatus:
					if item.Status != "" && entry.Status != item.Status {
						needsUpdate = true
						updateEntry.Status = item.Status
					} else {
						updateEntry.Status = entry.Status
					}
				case providers.FieldProgress:
					if item.Progress > entry.Progress {
						needsUpdate = true
						updateEntry.Progress = item.Progress
					} else {
						updateEntry.Progress = entry.Progress
					}
				}
			}
		}

		if !needsUpdate {
			skips++
			continue
		}

		updates++
		msg := fmt.Sprintf("Updated %s: status=%s, progress=%d", item.Title, updateEntry.Status, updateEntry.Progress)
		if err := listProv.UpdateEntry(ctx, updateEntry, fields, dryRun); err != nil {
			e.recordHistory(ctx, profile.Name, profile.ListProvider, item.ID, "sync", "error", err.Error(), dryRun)
			slog.Error("sync update failed", "title", item.Title, "error", err)
			continue
		}

		e.recordHistory(ctx, profile.Name, profile.ListProvider, item.ID, "sync", "success", msg, dryRun)
	}

	msg := fmt.Sprintf("profile completed: %d updates, %d skipped", updates, skips)
	e.hub.Publish("history", map[string]any{"profile": profile.Name, "status": "success", "message": msg, "dry_run": dryRun})
	slog.Info("sync profile completed", "profile", profile.Name, "updates", updates, "skips", skips)
	return nil
}

func (e *Engine) getAniListID(ctx context.Context, item providers.MediaItem) (int64, error) {
	// Check external IDs first
	if strID, ok := item.ExternalID["anilist"]; ok && strID != "" {
		if id, err := strconv.ParseInt(strID, 10, 64); err == nil && id > 0 {
			return id, nil
		}
	}

	if id, err := animap.ResolveAniList(ctx, e.db, item.ExternalID); err != nil {
		return 0, err
	} else if id > 0 {
		return id, nil
	}

	// Look up in animap table
	var anilistID sql.NullInt64
	err := sq.Select("anilist_id").From("animap").
		Where(sq.Eq{"provider": "library", "provider_id": item.ID}).
		RunWith(e.db).QueryRowContext(ctx).Scan(&anilistID)

	if err == sql.ErrNoRows {
		// Insert skeleton map for future mapping UI
		_, _ = sq.Insert("animap").
			Columns("provider", "provider_id", "title", "updated_at").
			Values("library", item.ID, item.Title, time.Now()).
			RunWith(e.db).ExecContext(ctx)
		return 0, nil
	} else if err != nil {
		return 0, err
	}

	return anilistID.Int64, nil
}

func (e *Engine) recordHistory(ctx context.Context, profile, provider, itemID, action, status, message string, dryRun bool) {
	_, _ = sq.Insert("sync_history").
		Columns("profile", "provider", "item_id", "action", "status", "message", "dry_run", "created_at").
		Values(profile, provider, itemID, action, status, message, dryRun, time.Now()).
		RunWith(e.db).ExecContext(ctx)
}
