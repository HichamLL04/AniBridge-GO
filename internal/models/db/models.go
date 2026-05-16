package db

import "time"

type AniMap struct {
	ID           int64     `json:"id"`
	Provider     string    `json:"provider"`
	ProviderID   string    `json:"provider_id"`
	AniListID    int64     `json:"anilist_id"`
	Title        string    `json:"title"`
	TitlesJSON   string    `json:"titles_json"`
	UserOverride bool      `json:"user_override"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type SyncHistory struct {
	ID        int64     `json:"id"`
	Profile   string    `json:"profile"`
	Provider  string    `json:"provider"`
	ItemID    string    `json:"item_id"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	DryRun    bool      `json:"dry_run"`
	CreatedAt time.Time `json:"created_at"`
}

type Pin struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Ref       string    `json:"ref"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

type Housekeeping struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
