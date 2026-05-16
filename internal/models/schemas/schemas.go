package schemas

import "time"

type APIError struct {
	Error string `json:"error"`
}

type ProfileState struct {
	Name     string     `json:"name"`
	Running  bool       `json:"running"`
	LastSync *time.Time `json:"last_sync,omitempty"`
	NextSync *time.Time `json:"next_sync,omitempty"`
	Errors   []string   `json:"errors"`
}

type ClientStatusResponse struct {
	Running  bool                    `json:"running"`
	Profiles map[string]ProfileState `json:"profiles"`
}

type ProfileConfigModel struct {
	LibraryNamespace string   `json:"library_namespace"`
	ListNamespace    string   `json:"list_namespace"`
	ScanModes        []string `json:"scan_modes"`
}

type ProfileRuntimeStatusModel struct {
	Running  bool       `json:"running"`
	LastSync *time.Time `json:"last_sync,omitempty"`
}

type ProfileStatusModel struct {
	Config ProfileConfigModel        `json:"config"`
	Status ProfileRuntimeStatusModel `json:"status"`
}

type StatusResponse struct {
	Profiles  map[string]ProfileStatusModel `json:"profiles"`
	Scheduler map[string]any                `json:"scheduler"`
}

type SystemResponse struct {
	Version   string `json:"version"`
	Uptime    string `json:"uptime"`
	RSSBytes  uint64 `json:"rss_bytes"`
	GoVersion string `json:"go_version"`
}

type TriggerSyncRequest struct {
	Profile string `json:"profile"`
	DryRun  *bool  `json:"dry_run,omitempty"`
}
