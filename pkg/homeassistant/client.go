package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Limits and timeouts (ADR-0001).
const (
	// StateTimeout bounds a single GET /api/states request.
	StateTimeout = 10 * time.Second
	// HistoryTimeout bounds a single GET /api/history/period request.
	HistoryTimeout = 30 * time.Second
	// ClientTimeout is the hard ceiling on any request of the shared http.Client.
	ClientTimeout = 30 * time.Second
	// StateBodyLimit is the maximum accepted size of a state response.
	StateBodyLimit int64 = 1 << 20 // 1 MiB
	// HistoryBodyLimit is the maximum accepted size of a history response.
	HistoryBodyLimit int64 = 16 << 20 // 16 MiB
	// MaxHistoryRange is the longest range one History call accepts; callers
	// loop over days.
	MaxHistoryRange = 25 * time.Hour
)

// Client is the read-only Home Assistant API used by sbam. It deliberately
// offers no generic request method (S-015).
type Client interface {
	State(ctx context.Context, entityID string) (State, error)
	History(ctx context.Context, entityID string, start, end time.Time) ([]HistoryPoint, error)
}

// HTTPClient implements Client over the HA REST API.
type HTTPClient struct {
	base  *url.URL
	token string
	http  *http.Client

	stateTimeout   time.Duration
	historyTimeout time.Duration
	stateLimit     int64
	historyLimit   int64
}

var _ Client = (*HTTPClient)(nil)

// New returns an HTTPClient for cfg. It validates the base URL and token.
func New(cfg Config) (*HTTPClient, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("homeassistant: token is required")
	}
	base, err := parseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	return &HTTPClient{
		base:  base,
		token: cfg.Token,
		http: &http.Client{
			Timeout: ClientTimeout,
			// Never follow redirects, so the token cannot reach another host (S-014).
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		stateTimeout:   StateTimeout,
		historyTimeout: HistoryTimeout,
		stateLimit:     StateBodyLimit,
		historyLimit:   HistoryBodyLimit,
	}, nil
}

// State fetches GET /api/states/<entityID>.
func (c *HTTPClient) State(ctx context.Context, entityID string) (State, error) {
	if err := ValidateEntityID(entityID); err != nil {
		return State{}, err
	}
	u := c.base.JoinPath("states", entityID)

	var st State
	if err := c.get(ctx, u, c.stateTimeout, c.stateLimit, &st); err != nil {
		return State{}, err
	}
	st.LastUpdated = st.LastUpdated.UTC()
	return st, nil
}

// historyEntry is one element of a minimal_response history series. Only the
// first element carries entity_id.
type historyEntry struct {
	EntityID    string    `json:"entity_id"`
	State       string    `json:"state"`
	LastChanged time.Time `json:"last_changed"`
}

// History fetches the recorder history of entityID in [start, end] via
// GET /api/history/period/<start>. The range must not exceed MaxHistoryRange.
// The returned points include HA's initial state at start.
func (c *HTTPClient) History(ctx context.Context, entityID string, start, end time.Time) ([]HistoryPoint, error) {
	if err := ValidateEntityID(entityID); err != nil {
		return nil, err
	}
	if !end.After(start) {
		return nil, errors.New("homeassistant: history end must be after start")
	}
	if end.Sub(start) > MaxHistoryRange {
		return nil, fmt.Errorf("homeassistant: history range %s exceeds %s", end.Sub(start), MaxHistoryRange)
	}

	u := c.base.JoinPath("history", "period", start.UTC().Format(time.RFC3339))
	q := url.Values{}
	q.Set("filter_entity_id", entityID)
	q.Set("end_time", end.UTC().Format(time.RFC3339))
	q.Set("significant_changes_only", "0")
	// HA checks presence of these flags only; send them value-less.
	u.RawQuery = q.Encode() + "&minimal_response&no_attributes"

	var series [][]historyEntry
	if err := c.get(ctx, u, c.historyTimeout, c.historyLimit, &series); err != nil {
		return nil, err
	}

	points := []HistoryPoint{}
	for _, s := range series {
		if len(s) == 0 || s[0].EntityID != entityID {
			continue
		}
		for _, e := range s {
			points = append(points, HistoryPoint{State: e.State, LastChanged: e.LastChanged.UTC()})
		}
	}
	return points, nil
}

// get issues a GET, checks the status before decoding, and stream-decodes the
// body into out under a size limit.
func (c *HTTPClient) get(ctx context.Context, u *url.URL, timeout time.Duration, limit int64, out any) error {
	path := u.EscapedPath()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := c.newRequest(ctx, u)
	if err != nil {
		return fmt.Errorf("homeassistant: GET %s: build request: %w", path, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("homeassistant: GET %s: %w", path, transportErr(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if err := statusErr(resp.StatusCode, path); err != nil {
		// Drain a bounded amount so the connection can be reused; never read it into errors.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return err
	}

	dec := json.NewDecoder(&limitedReader{r: resp.Body, remaining: limit})
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, ErrTooLarge) {
			return fmt.Errorf("homeassistant: GET %s: body exceeds %d bytes: %w", path, limit, ErrTooLarge)
		}
		var syn *json.SyntaxError
		var typ *json.UnmarshalTypeError
		if errors.As(err, &syn) || errors.As(err, &typ) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return fmt.Errorf("homeassistant: GET %s: decode response: invalid JSON", path)
		}
		return fmt.Errorf("homeassistant: GET %s: read response: %w", path, transportErr(err))
	}
	return nil
}

// newRequest is the only place the Authorization header is set (S-013).
func (c *HTTPClient) newRequest(ctx context.Context, u *url.URL) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// statusErr maps a status code to a sentinel error carrying status and path.
func statusErr(code int, path string) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("homeassistant: GET %s: status %d: %w", path, code, ErrAuth)
	case code == http.StatusNotFound:
		return fmt.Errorf("homeassistant: GET %s: status %d: %w", path, code, ErrNotFound)
	case code >= 300 && code < 400:
		return fmt.Errorf("homeassistant: GET %s: status %d (redirect not followed): %w", path, code, ErrStatus)
	default:
		return fmt.Errorf("homeassistant: GET %s: status %d: %w", path, code, ErrStatus)
	}
}

// transportErr strips *url.Error so the full request URL (with query) does
// not end up in error strings.
func transportErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// limitedReader passes through at most remaining bytes and returns
// ErrTooLarge if the underlying reader has any data beyond that. Bytes past
// the limit are never handed to the caller.
type limitedReader struct {
	r         io.Reader
	remaining int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if l.remaining <= 0 {
		var probe [1]byte
		n, err := l.r.Read(probe[:])
		if n > 0 {
			return 0, ErrTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	return n, err
}
