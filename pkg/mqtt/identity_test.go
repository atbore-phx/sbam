package mqtt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keeps identity-file writes (e.g. via PublishDiscovery) out of the repo tree.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sbam-mqtt-identity-")
	if err == nil {
		identityFilePath = filepath.Join(dir, "mqtt_device_id")
	}
	code := m.Run()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

func overrideIdentityFile(t *testing.T) string {
	t.Helper()
	old := identityFilePath
	identityFilePath = filepath.Join(t.TempDir(), "mqtt_device_id")
	t.Cleanup(func() { identityFilePath = old })
	return identityFilePath
}

func TestEnsureStableDeviceIDSeedsFileFromConfig(t *testing.T) {
	path := overrideIdentityFile(t)
	cfg := Config{FroniusIP: "192.168.1.10"}

	id := EnsureStableDeviceID(cfg)

	assert.Equal(t, discoveryDeviceIdentifier(cfg), id)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, id+"\n", string(data))
}

func TestEnsureStableDeviceIDSurvivesFroniusIPChange(t *testing.T) {
	overrideIdentityFile(t)
	first := EnsureStableDeviceID(Config{FroniusIP: "192.168.1.10"})

	second := EnsureStableDeviceID(Config{FroniusIP: "10.0.40.20"})

	assert.Equal(t, first, second)
	assert.NotEqual(t, discoveryDeviceIdentifier(Config{FroniusIP: "10.0.40.20"}), second)
}

func TestEnsureStableDeviceIDManualOverride(t *testing.T) {
	path := overrideIdentityFile(t)
	require.NoError(t, os.WriteFile(path, []byte("sbam_pinned-id\n"), 0o644))

	id := EnsureStableDeviceID(Config{FroniusIP: "192.168.1.10"})

	assert.Equal(t, "sbam_pinned-id", id)
}

func TestEnsureStableDeviceIDInvalidFileReseeded(t *testing.T) {
	path := overrideIdentityFile(t)
	require.NoError(t, os.WriteFile(path, []byte("not a valid id\nsecond line"), 0o644))
	cfg := Config{FroniusIP: "192.168.1.10"}

	id := EnsureStableDeviceID(cfg)

	assert.Equal(t, discoveryDeviceIdentifier(cfg), id)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, id+"\n", string(data))
}

func TestEnsureStableDeviceIDUnreadableFileFallsBack(t *testing.T) {
	old := identityFilePath
	// a directory: reading fails with a non-IsNotExist error
	identityFilePath = t.TempDir()
	t.Cleanup(func() { identityFilePath = old })
	cfg := Config{FroniusIP: "192.168.1.10"}

	assert.Equal(t, discoveryDeviceIdentifier(cfg), EnsureStableDeviceID(cfg))
}

func TestEnsureStableDeviceIDUnwritablePathFallsBack(t *testing.T) {
	old := identityFilePath
	identityFilePath = filepath.Join(t.TempDir(), "missing", "mqtt_device_id")
	t.Cleanup(func() { identityFilePath = old })
	cfg := Config{FroniusIP: "192.168.1.10"}

	assert.Equal(t, discoveryDeviceIdentifier(cfg), EnsureStableDeviceID(cfg))
}

func TestBuildDiscoveryWithDeviceIDOverride(t *testing.T) {
	cfg := Config{Enabled: true, HADiscovery: true, FroniusIP: "192.168.1.10"}

	entities := BuildDiscoveryWithDeviceID(cfg, "1.0.0", "sbam_pinned-id")

	require.NotEmpty(t, entities)
	for _, entity := range entities {
		var payload map[string]any
		require.NoError(t, json.Unmarshal(entity.Payload, &payload))
		uid, _ := payload["unique_id"].(string)
		assert.True(t, strings.HasPrefix(uid, "sbam_pinned-id_"), "unique_id %q", uid)
	}
}

func TestBuildDiscoveryWithDeviceIDEmptyFallsBackToDerived(t *testing.T) {
	cfg := Config{Enabled: true, HADiscovery: true, FroniusIP: "192.168.1.10"}

	assert.Equal(t, BuildDiscovery(cfg, "1.0.0"), BuildDiscoveryWithDeviceID(cfg, "1.0.0", "  "))
}
