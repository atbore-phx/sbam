package storage

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func powerFlowServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, powerFlowURL, r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// Shape of a real Gen24 answer (values from a live read, rounded).
const livePowerFlow = `{"Body":{"Data":{"Site":{"Meter_Location":"grid","Mode":"bidirectional","P_Akku":-207.5,"P_Grid":7.4,"P_Load":-1925.4,"P_PV":2238.2}}},"Head":{"Status":{"Code":0,"Reason":"","UserMessage":""}}}`

func TestReadPowerFlow(t *testing.T) {
	ip := powerFlowServer(t, http.StatusOK, livePowerFlow)

	p, err := ReadPowerFlow(ip)

	require.NoError(t, err)
	assert.InDelta(t, 7.4, p.GridW, 0.001)
	assert.InDelta(t, -207.5, p.BatteryW, 0.001)
	assert.InDelta(t, 7.4-207.5, p.BaselineImportW(), 0.001, "charging power is removed from the import")
}

func TestReadPowerFlowBaselineWhileDischarging(t *testing.T) {
	ip := powerFlowServer(t, http.StatusOK, `{"Body":{"Data":{"Site":{"P_Akku":800,"P_Grid":100}}},"Head":{"Status":{"Code":0}}}`)

	p, err := ReadPowerFlow(ip)

	require.NoError(t, err)
	assert.InDelta(t, 900, p.BaselineImportW(), 0.001, "with the battery idle the grid would supply its 800 W too")
}

func TestReadPowerFlowNoBattery(t *testing.T) {
	ip := powerFlowServer(t, http.StatusOK, `{"Body":{"Data":{"Site":{"P_Akku":null,"P_Grid":1500}}},"Head":{"Status":{"Code":0}}}`)

	p, err := ReadPowerFlow(ip)

	require.NoError(t, err)
	assert.Zero(t, p.BatteryW)
	assert.InDelta(t, 1500, p.BaselineImportW(), 0.001)
}

func TestReadPowerFlowErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"no meter":     {http.StatusOK, `{"Body":{"Data":{"Site":{"P_Grid":null}}},"Head":{"Status":{"Code":0}}}`, "no grid power"},
		"api status":   {http.StatusOK, `{"Body":{},"Head":{"Status":{"Code":8,"Reason":"busy"}}}`, "status 8"},
		"http error":   {http.StatusInternalServerError, ``, "HTTP 500"},
		"invalid json": {http.StatusOK, `{`, "decode power flow"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ip := powerFlowServer(t, tc.status, tc.body)

			_, err := ReadPowerFlow(ip)

			assert.ErrorContains(t, err, tc.want)
		})
	}

	_, err := ReadPowerFlow("127.0.0.1:1")
	assert.ErrorContains(t, err, "read power flow")
}
