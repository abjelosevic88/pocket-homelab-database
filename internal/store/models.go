package store

import "strings"

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Connection is one database a person can open. The password never leaves
// the server: Password holds it sealed, HasPassword says whether there is one.
type Connection struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"-"`
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Password     string `json:"-"`
	HasPassword  bool   `json:"has_password"`
	Database     string `json:"database"`
	SSLMode      string `json:"ssl_mode"`
	Path         string `json:"path"`
	HostPath     string `json:"host_path"`
	Container    string `json:"container"`
	Group        string `json:"group"`
	Color        string `json:"color"`
	Notes        string `json:"notes"`
	AllowWrites  bool   `json:"allow_writes"`
	Favorite     bool   `json:"favorite"`
	Monitor      bool   `json:"monitor"`
	Source       string `json:"source"`
	DiscoveryKey string `json:"discovery_key"`
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail"`
	Version      string `json:"version"`
	SizeBytes    int64  `json:"size_bytes"`
	LatencyMs    int64  `json:"latency_ms"`
	CheckedAt    string `json:"checked_at"`
	DownSince    string `json:"down_since"`
	Fails        int    `json:"-"`
	Alerted      bool   `json:"-"`
	OpenedAt     string `json:"opened_at"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// SavedQuery is a query a person keeps.
type SavedQuery struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"-"`
	ConnectionID *int64 `json:"connection_id"`
	Name         string `json:"name"`
	Database     string `json:"database"`
	Body         string `json:"body"`
	Description  string `json:"description"`
	Favorite     bool   `json:"favorite"`
	RunCount     int    `json:"run_count"`
	LastRunAt    string `json:"last_run_at"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// HistoryEntry is one run of the SQL editor or the Redis console.
type HistoryEntry struct {
	ID           int64  `json:"id"`
	ConnectionID int64  `json:"connection_id"`
	Connection   string `json:"connection"`
	Engine       string `json:"engine"`
	Database     string `json:"database"`
	Body         string `json:"body"`
	OK           bool   `json:"ok"`
	Error        string `json:"error"`
	Rows         int64  `json:"rows"`
	DurationMs   int64  `json:"duration_ms"`
	CreatedAt    string `json:"created_at"`
}

// Activity is one change made through the app.
type Activity struct {
	ID             int64  `json:"id"`
	ConnectionID   *int64 `json:"connection_id"`
	ConnectionName string `json:"connection_name"`
	Kind           string `json:"kind"`
	Database       string `json:"database"`
	Target         string `json:"target"`
	Summary        string `json:"summary"`
	Statement      string `json:"statement"`
	Rows           int64  `json:"rows"`
	OK             bool   `json:"ok"`
	Error          string `json:"error"`
	SnapshotID     *int64 `json:"snapshot_id"`
	Source         string `json:"source"`
	CreatedAt      string `json:"created_at"`
}

// Snapshot is a copy of a SQLite file taken before a change.
type Snapshot struct {
	ID           int64  `json:"id"`
	ConnectionID int64  `json:"connection_id"`
	Dir          string `json:"-"`
	File         string `json:"file"`
	SizeBytes    int64  `json:"size_bytes"`
	Reason       string `json:"reason"`
	RestoredAt   string `json:"restored_at"`
	CreatedAt    string `json:"created_at"`
}

// Maintenance is a live SQLite file opened for changes.
type Maintenance struct {
	ID           int64    `json:"id"`
	UserID       int64    `json:"-"`
	ConnectionID int64    `json:"connection_id"`
	Containers   []string `json:"containers"`
	SnapshotID   *int64   `json:"snapshot_id"`
	StartedAt    string   `json:"started_at"`
	ExpiresAt    string   `json:"expires_at"`
	EndedAt      string   `json:"ended_at"`
	EndReason    string   `json:"end_reason"`
	Error        string   `json:"error"`
}

// SizePoint is one day's size.
type SizePoint struct {
	ConnectionID int64  `json:"connection_id"`
	Date         string `json:"date"`
	SizeBytes    int64  `json:"size_bytes"`
}

// Settings are the box-wide values plus one person's preferences.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`

	// Browsing and the SQL editor.
	PageSize      int    `json:"page_size"`       // rows per page of a table
	RowLimit      int    `json:"row_limit"`       // rows a query shows at most
	QueryTimeoutS int    `json:"query_timeout_s"` // a query is stopped after this
	ConfirmWrites bool   `json:"confirm_writes"`  // ask before any statement that changes something
	EditorFont    int    `json:"editor_font"`     // px
	Density       string `json:"density"`         // compact | comfortable
	ShowSystem    bool   `json:"show_system"`     // system databases and schemas in the tree

	// Safety for live SQLite files.
	PauseMinutes int `json:"pause_minutes"` // how long an app stays paused for changes
	SnapshotKeep int `json:"snapshot_keep"` // snapshots kept per connection

	// Discovery.
	ShowBackups bool `json:"show_backups"` // list backup copies of SQLite files too

	// Watching: connections with "monitor" on are checked every five minutes.
	NotifyDown      bool   `json:"notify_down"`
	NotifyRecovered bool   `json:"notify_recovered"`
	NotifyURL       string `json:"notify_url"`
	NotifyFormat    string `json:"notify_format"` // apprise | ntfy | json

	// AI writes and explains queries: Pocket AI through Core, or an
	// OpenAI-compatible API with the person's own key.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

// SplitTags turns "a, b ,c" into clean lowercase tags.
func SplitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// JoinTags is the stored form of tags.
func JoinTags(s string) string { return strings.Join(SplitTags(s), ", ") }
