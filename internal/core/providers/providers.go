package providers

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type SyncField string

const (
	FieldStatus     SyncField = "status"
	FieldProgress   SyncField = "progress"
	FieldRepeats    SyncField = "repeats"
	FieldReview     SyncField = "review"
	FieldUserRating SyncField = "user_rating"
	FieldStartedAt  SyncField = "started_at"
	FieldFinishedAt SyncField = "finished_at"
)

type MediaItem struct {
	ID         string
	Title      string
	Type       string
	Progress   int
	Episodes   int
	Status     string
	UpdatedAt  time.Time
	ExternalID map[string]string
}

type ListEntry struct {
	ID         string
	AniListID  int64
	Status     string
	Progress   int
	Repeats    int
	Review     string
	UserRating *float64
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type LibraryProvider interface {
	Namespace() string
	Initialize(context.Context) error
	Scan(context.Context) ([]MediaItem, error)
	HandleWebhook(context.Context, []byte) error
}

type ListProvider interface {
	Namespace() string
	Initialize(context.Context) error
	GetEntry(context.Context, int64) (*ListEntry, error)
	UpdateEntry(context.Context, ListEntry, []SyncField, bool) error
}

type Factory func(namespace string, settings map[string]any) (any, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

func Register(kind, namespace string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[kind+":"+namespace] = factory
}

func Build(kind, namespace string, settings map[string]any) (any, error) {
	mu.RLock()
	factory := registry[kind+":"+namespace]
	mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("provider %s/%s is not registered", kind, namespace)
	}
	return factory(namespace, settings)
}

func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}
