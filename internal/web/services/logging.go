package services

import "sync"

type LogEntry struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

type LogStore struct {
	mu    sync.RWMutex
	limit int
	items []LogEntry
	hub   *Hub
}

func NewLogStore(limit int, hub *Hub) *LogStore { return &LogStore{limit: limit, hub: hub} }

func (s *LogStore) Add(entry LogEntry) {
	s.mu.Lock()
	s.items = append(s.items, entry)
	if len(s.items) > s.limit {
		s.items = s.items[len(s.items)-s.limit:]
	}
	s.mu.Unlock()
	if s.hub != nil {
		s.hub.Publish("logs", entry)
	}
}

func (s *LogStore) List() []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]LogEntry, len(s.items))
	copy(out, s.items)
	return out
}
