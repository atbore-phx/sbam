package homeassistant

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testToken       = "test-ha-token"
	priceEntity     = "sensor.nordpool_kwh_dk1_dkk_3_10_025"
	loadEntity      = "sensor.house_load_power"
	historyPathPfx  = "/api/history/period/"
	statesPathPfx   = "/api/states/"
	echoAuthBodyFmt = `{"message":"denied","echo":"%s"}`
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func newTestClient(t *testing.T, serverURL string) *HTTPClient {
	t.Helper()
	c, err := New(Config{BaseURL: serverURL + "/api", Token: testToken, Source: SourceURL})
	require.NoError(t, err)
	return c
}

var (
	histStart = time.Date(2026, 10, 1, 22, 0, 0, 0, time.UTC)
	histEnd   = histStart.Add(24 * time.Hour)
)

func TestNew(t *testing.T) {
	c, err := New(Config{BaseURL: "http://supervisor/core/api", Token: "test-sup-token", Source: SourceSupervisor})
	require.NoError(t, err)
	assert.Equal(t, ClientTimeout, c.http.Timeout)
	assert.Equal(t, 30*time.Second, c.http.Timeout)
	assert.Equal(t, 30*time.Second, c.historyTimeout)
	assert.Equal(t, 10*time.Second, c.stateTimeout)
	assert.NotNil(t, c.http.CheckRedirect)

	_, err = New(Config{BaseURL: "http://supervisor/core/api"})
	assert.ErrorContains(t, err, "token is required")

	_, err = New(Config{BaseURL: "ftp://x/api", Token: testToken})
	assert.ErrorContains(t, err, "scheme")

	_, err = New(Config{BaseURL: "http://u:p@h/api", Token: testToken})
	assert.ErrorContains(t, err, "userinfo")
}

// T-0081 (AC-1): history fixture is parsed, the initial state is included,
// and the request carries the expected path, query and bearer header.
func TestHistory_T0081(t *testing.T) {
	fixture := readFixture(t, "history_minimal.json")
	var gotReq *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	points, err := c.History(context.Background(), loadEntity, histStart, histEnd)
	require.NoError(t, err)

	require.Len(t, points, 4)
	assert.Equal(t, HistoryPoint{State: "512.4", LastChanged: histStart}, points[0], "initial state at start")
	assert.Equal(t, "640.0", points[1].State)
	assert.Equal(t, time.Date(2026, 10, 1, 22, 17, 31, 204511000, time.UTC), points[1].LastChanged.UTC())
	assert.Equal(t, "unavailable", points[2].State)
	assert.Equal(t, "455.9", points[3].State)

	require.NotNil(t, gotReq)
	assert.Equal(t, http.MethodGet, gotReq.Method)
	assert.Equal(t, "/api/history/period/2026-10-01T22:00:00Z", gotReq.URL.Path)
	q := gotReq.URL.Query()
	assert.Equal(t, loadEntity, q.Get("filter_entity_id"))
	assert.Equal(t, "2026-10-02T22:00:00Z", q.Get("end_time"))
	assert.Equal(t, "0", q.Get("significant_changes_only"))
	assert.True(t, q.Has("minimal_response"))
	assert.True(t, q.Has("no_attributes"))
	assert.Contains(t, gotReq.URL.RawQuery, "&minimal_response&no_attributes")
	assert.Equal(t, "Bearer "+testToken, gotReq.Header.Get("Authorization"))
}

// T-0081 edge: non-UTC times are sent as UTC; an empty history is not an error;
// series for other entities are ignored.
func TestHistory_EdgeCases(t *testing.T) {
	var body atomic.Value
	var path atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	c := newTestClient(t, server.URL)

	cph := time.FixedZone("CEST", 2*3600)
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, cph)

	body.Store(`[]`)
	points, err := c.History(context.Background(), loadEntity, start, start.Add(24*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, points)
	assert.NotNil(t, points)
	assert.Equal(t, "/api/history/period/2026-10-01T22:00:00Z", path.Load())

	body.Store(`[[{"entity_id":"sensor.other","state":"1","last_changed":"2026-10-01T22:00:00+00:00"}]]`)
	points, err = c.History(context.Background(), loadEntity, start, start.Add(time.Hour))
	require.NoError(t, err)
	assert.Empty(t, points)

	body.Store(`{"not":"an array"}`)
	_, err = c.History(context.Background(), loadEntity, start, start.Add(time.Hour))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")

	body.Store(`[[{"entity_id":"sensor.house_load_power","state":"1","last_changed":"not-a-time"}]]`)
	_, err = c.History(context.Background(), loadEntity, start, start.Add(time.Hour))
	require.Error(t, err)
}

// T-0082 (AC-5): state fixture is parsed including attributes and last_updated.
func TestState_T0082(t *testing.T) {
	fixture := readFixture(t, "state_price.json")
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	st, err := c.State(context.Background(), priceEntity)
	require.NoError(t, err)

	assert.Equal(t, "/api/states/"+priceEntity, gotPath)
	assert.Equal(t, "Bearer "+testToken, gotAuth)
	assert.Equal(t, priceEntity, st.EntityID)
	assert.Equal(t, "1.234", st.State)
	assert.Equal(t, time.Date(2026, 10, 2, 9, 0, 0, 123456000, time.UTC), st.LastUpdated.UTC())
	assert.Equal(t, "DKK/kWh", st.Attributes["unit_of_measurement"])
	assert.Equal(t, true, st.Attributes["tomorrow_valid"])
	today, ok := st.Attributes["raw_today"].([]any)
	require.True(t, ok)
	assert.Len(t, today, 2)
	first, ok := today[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 1.111, first["value"], 1e-9)
}

func TestState_InvalidEntity(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()
	c := newTestClient(t, server.URL)

	_, err := c.State(context.Background(), "sensor.a&end_time=2000")
	assert.Error(t, err)
	_, err = c.History(context.Background(), "../states/sensor.a", histStart, histEnd)
	assert.Error(t, err)
	assert.Equal(t, int32(0), hits.Load())
}

// infiniteHistory streams a syntactically valid, never-ending history array.
func infiniteHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`[[{"entity_id":"sensor.house_load_power","state":"1","last_changed":"2026-10-01T22:00:00+00:00"}`))
	chunk := bytes.Repeat([]byte(`,{"state":"512.4","last_changed":"2026-10-01T22:00:00+00:00"}`), 1024)
	for written := 0; written < 100<<20; written += len(chunk) {
		if r.Context().Err() != nil {
			return
		}
		if _, err := w.Write(chunk); err != nil {
			return
		}
	}
	_, _ = w.Write([]byte(`]]`))
}

// T-0083 (TC-012a, S-012): a 100 MiB body fails with ErrTooLarge at the limit.
func TestHistory_TooLarge_T0083(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(infiniteHistory))
	defer server.Close()

	c := newTestClient(t, server.URL)
	assert.Equal(t, int64(16<<20), c.historyLimit)

	_, err := c.History(context.Background(), loadEntity, histStart, histEnd)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTooLarge)
	assert.Contains(t, err.Error(), historyPathPfx)
}

func TestState_TooLarge_T0083(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entity_id":"sensor.a","state":"`))
		_, _ = w.Write(bytes.Repeat([]byte("x"), int(StateBodyLimit)+10))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	_, err := c.State(context.Background(), "sensor.a")
	assert.ErrorIs(t, err, ErrTooLarge)
}

func TestLimitedReader_ExactLimitIsAccepted(t *testing.T) {
	body := `{"entity_id":"sensor.a","state":"1","attributes":{},"last_updated":"2026-10-02T09:00:00+00:00"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	c.stateLimit = int64(len(body))
	_, err := c.State(context.Background(), "sensor.a")
	assert.NoError(t, err)

	c.stateLimit = int64(len(body)) - 1
	_, err = c.State(context.Background(), "sensor.a")
	assert.ErrorIs(t, err, ErrTooLarge)
}

// T-0084 (TC-012b): a server that never answers is cut off by the per-request
// timeout. The production timeout is 30 s (asserted in TestNew); the test
// shortens it to avoid wall-clock waits.
func TestHistory_Timeout_T0084(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	c := newTestClient(t, server.URL)
	c.historyTimeout = 100 * time.Millisecond

	begin := time.Now()
	_, err := c.History(context.Background(), loadEntity, histStart, histEnd)
	elapsed := time.Since(begin)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 5*time.Second)
	assert.Contains(t, err.Error(), historyPathPfx)
	assert.NotContains(t, err.Error(), "filter_entity_id")
}

func TestState_CallerContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.State(ctx, "sensor.a")
	assert.ErrorIs(t, err, context.Canceled)
}

// T-0085 (TC-012c, TC-013c, S-013): error responses map to sentinels, and the
// error string carries status and path only — never the body or the token.
func TestErrorStatus_T0085(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrAuth},
		{http.StatusForbidden, ErrAuth},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusInternalServerError, ErrStatus},
		{http.StatusServiceUnavailable, ErrStatus},
		{http.StatusNoContent, ErrStatus},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(strings.ReplaceAll(echoAuthBodyFmt, "%s", r.Header.Get("Authorization"))))
			}))
			defer server.Close()
			c := newTestClient(t, server.URL)

			_, err := c.State(context.Background(), priceEntity)
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.want)
			assertSafeError(t, err, statesPathPfx+priceEntity, tt.status)

			_, err = c.History(context.Background(), loadEntity, histStart, histEnd)
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.want)
			assertSafeError(t, err, historyPathPfx, tt.status)
		})
	}
}

func assertSafeError(t *testing.T, err error, path string, status int) {
	t.Helper()
	msg := err.Error()
	assert.Contains(t, msg, path)
	assert.Contains(t, msg, "status "+strconv.Itoa(status))
	assert.NotContains(t, msg, testToken)
	assert.NotContains(t, msg, "Bearer")
	assert.NotContains(t, msg, "denied")
	assert.NotContains(t, msg, "echo")
}

// T-0086 (TC-014b, S-014): a 302 to a second host is not followed.
func TestRedirectNotFollowed_T0086(t *testing.T) {
	var secondHits atomic.Int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer second.Close()

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+r.URL.RequestURI(), http.StatusFound)
	}))
	defer first.Close()

	c := newTestClient(t, first.URL)
	_, err := c.State(context.Background(), priceEntity)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStatus)
	assert.Contains(t, err.Error(), "302")
	assert.Contains(t, err.Error(), "redirect not followed")

	_, err = c.History(context.Background(), loadEntity, histStart, histEnd)
	assert.ErrorIs(t, err, ErrStatus)

	assert.Equal(t, int32(0), secondHits.Load())
}

// T-0088 (TC-015a, S-015): every request is a GET on an allowlisted path, and
// the API surface is exactly State and History.
func TestGetOnlyAllowlist_T0088(t *testing.T) {
	stateFixture := readFixture(t, "state_price.json")
	historyFixture := readFixture(t, "history_minimal.json")

	type seen struct{ method, path string }
	var mu sync.Mutex
	var reqs []seen
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqs = append(reqs, seen{r.Method, r.URL.Path})
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, historyPathPfx) {
			_, _ = w.Write(historyFixture)
			return
		}
		_, _ = w.Write(stateFixture)
	}))
	defer server.Close()

	var c Client = newTestClient(t, server.URL)
	_, err := c.State(context.Background(), priceEntity)
	require.NoError(t, err)
	_, err = c.History(context.Background(), loadEntity, histStart, histEnd)
	require.NoError(t, err)
	_, _ = c.State(context.Background(), "sensor.a&x=1")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, reqs, 2)
	for _, r := range reqs {
		assert.Equal(t, http.MethodGet, r.method)
		assert.True(t, strings.HasPrefix(r.path, statesPathPfx) || strings.HasPrefix(r.path, historyPathPfx), r.path)
	}

	iface := reflect.TypeOf((*Client)(nil)).Elem()
	assert.Equal(t, 2, iface.NumMethod())
	impl := reflect.TypeOf(&HTTPClient{})
	names := []string{}
	for i := 0; i < impl.NumMethod(); i++ {
		names = append(names, impl.Method(i).Name)
	}
	assert.ElementsMatch(t, []string{"History", "State"}, names)
}

// T-0089 (ADR-0001): history ranges above 25 h (or non-positive) are rejected
// without a request.
func TestHistory_RangeLimit_T0089(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	c := newTestClient(t, server.URL)

	_, err := c.History(context.Background(), loadEntity, histStart, histStart.Add(25*time.Hour+time.Second))
	assert.ErrorContains(t, err, "exceeds")

	_, err = c.History(context.Background(), loadEntity, histStart, histStart)
	assert.Error(t, err)

	_, err = c.History(context.Background(), loadEntity, histStart, histStart.Add(-time.Hour))
	assert.Error(t, err)
	assert.Equal(t, int32(0), hits.Load())

	// Exactly 25 h (a DST fall-back day) is allowed.
	_, err = c.History(context.Background(), loadEntity, histStart, histStart.Add(25*time.Hour))
	assert.NoError(t, err)
	assert.Equal(t, int32(1), hits.Load())
}
