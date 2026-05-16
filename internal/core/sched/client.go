package sched

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	 "anibridge-go/internal/config"
	coresync  "anibridge-go/internal/core/sync"
	 "anibridge-go/internal/models/schemas"
	 "anibridge-go/internal/web/services"
)

type Client struct {
	cfg     config.Config
	db      *sql.DB
	hub     *services.Hub
	engine  *coresync.Engine
	cron    *cron.Cron
	mu      sync.RWMutex
	running bool
	states  map[string]schemas.ProfileState
	cancel  context.CancelFunc
}

func NewClient(cfg config.Config, db *sql.DB, hub *services.Hub) *Client {
	return &Client{cfg: cfg, db: db, hub: hub, engine: coresync.NewEngine(cfg, db, hub), cron: cron.New(), states: map[string]schemas.ProfileState{}}
}

func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.cfg.Profiles {
		st := schemas.ProfileState{Name: p.Name, Errors: []string{}}
		if c.cfg.ScanMode == config.ScanPeriodic {
			next := time.Now().Add(c.cfg.ScanInterval.Duration)
			st.NextSync = &next
		}
		c.states[p.Name] = st
	}
	return nil
}

func (c *Client) Start() error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.running = true
	mode := c.cfg.ScanMode
	interval := c.cfg.ScanInterval.Duration
	if mode == config.ScanPoll {
		interval = c.cfg.PollInterval.Duration
	}
	c.mu.Unlock()

	if mode == config.ScanPeriodic || mode == config.ScanPoll {
		go c.loop(ctx, interval)
	}
	c.publish()
	return nil
}

func (c *Client) Stop(ctx context.Context) error {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.running = false
	c.mu.Unlock()
	c.publish()
	return nil
}

func (c *Client) Trigger(ctx context.Context, profileName string, dryRun *bool) error {
	var wg sync.WaitGroup
	for _, p := range c.cfg.Profiles {
		if profileName != "" && p.Name != profileName {
			continue
		}
		p := p
		runDry := c.cfg.DryRun
		if p.DryRun != nil {
			runDry = *p.DryRun
		}
		if dryRun != nil {
			runDry = *dryRun
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.runOne(ctx, p, runDry)
		}()
	}
	wg.Wait()
	return nil
}

func (c *Client) Status() schemas.ClientStatusResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()
	copyStates := map[string]schemas.ProfileState{}
	for k, v := range c.states {
		copyStates[k] = v
	}
	return schemas.ClientStatusResponse{Running: c.running, Profiles: copyStates}
}

func (c *Client) loop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.Trigger(ctx, "", nil)
		}
	}
}

func (c *Client) runOne(ctx context.Context, p config.ProfileConfig, dryRun bool) {
	now := time.Now()
	c.mu.Lock()
	st := c.states[p.Name]
	st.Running = true
	c.states[p.Name] = st
	c.mu.Unlock()
	c.publish()

	err := c.engine.RunProfile(ctx, p, dryRun)
	c.mu.Lock()
	st = c.states[p.Name]
	st.Running = false
	st.LastSync = &now
	next := time.Now().Add(c.cfg.ScanInterval.Duration)
	st.NextSync = &next
	if err != nil {
		st.Errors = append(st.Errors, err.Error())
	}
	c.states[p.Name] = st
	c.mu.Unlock()
	c.publish()
}

func (c *Client) publish() {
	st := c.Status()
	profiles := make(map[string]schemas.ProfileStatusModel)
	for name, p := range st.Profiles {
		var libNs, listNs string
		for _, cfgProfile := range c.cfg.Profiles {
			if cfgProfile.Name == name {
				libNs = cfgProfile.LibraryProvider
				listNs = cfgProfile.ListProvider
				break
			}
		}
		profiles[name] = schemas.ProfileStatusModel{
			Config: schemas.ProfileConfigModel{LibraryNamespace: libNs, ListNamespace: listNs, ScanModes: []string{"periodic"}},
			Status: schemas.ProfileRuntimeStatusModel{Running: p.Running, LastSync: p.LastSync},
		}
	}
	c.hub.Publish("status", schemas.StatusResponse{Profiles: profiles, Scheduler: map[string]any{"running": st.Running}})
}
