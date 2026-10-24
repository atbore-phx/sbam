// Package homeassistant is a small, GET-only client for the Home Assistant
// Core REST API (ADR-0001). It reads entity states and recorder history and
// returns raw HA values; it contains no price or consumption logic.
//
// The package never logs. Errors carry the HTTP status code and request path
// only, never response bodies, headers or the access token.
package homeassistant

import (
	"errors"
	"fmt"
	"time"
)

// Access sources reported in Config.Source.
const (
	// SourceSupervisor means the Supervisor-injected token and proxy URL are used.
	SourceSupervisor = "supervisor"
	// SourceURL means the user-configured ha_url and ha_token are used.
	SourceURL = "url"
)

// Sentinel errors returned (wrapped) by the client.
var (
	// ErrAuth is returned for HTTP 401 and 403 responses.
	ErrAuth = errors.New("homeassistant: authentication failed")
	// ErrNotFound is returned for HTTP 404 responses.
	ErrNotFound = errors.New("homeassistant: not found")
	// ErrStatus is returned for any other non-200 response, including 3xx.
	ErrStatus = errors.New("homeassistant: unexpected status")
	// ErrTooLarge is returned when a response body exceeds its size limit.
	ErrTooLarge = errors.New("homeassistant: response too large")
)

// Config holds the resolved Home Assistant access parameters.
type Config struct {
	// BaseURL is the API root and always ends in "/api".
	BaseURL string
	// Token is the bearer token. It is a secret and must never be logged.
	Token string
	// Source is SourceSupervisor or SourceURL.
	Source string
}

// String implements fmt.Stringer and redacts the token.
func (c Config) String() string {
	return fmt.Sprintf("{BaseURL:%s Token:%s Source:%s}", c.BaseURL, redact(c.Token), c.Source)
}

// GoString implements fmt.GoStringer and redacts the token.
func (c Config) GoString() string {
	return fmt.Sprintf("homeassistant.Config{BaseURL:%q, Token:%q, Source:%q}", c.BaseURL, redact(c.Token), c.Source)
}

func redact(s string) string {
	if s == "" {
		return ""
	}
	return "***"
}

// State is an entity state as returned by GET /api/states/<entity_id>.
type State struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastUpdated time.Time      `json:"last_updated"`
}

// HistoryPoint is one state change from GET /api/history/period.
type HistoryPoint struct {
	State       string    `json:"state"`
	LastChanged time.Time `json:"last_changed"`
}
