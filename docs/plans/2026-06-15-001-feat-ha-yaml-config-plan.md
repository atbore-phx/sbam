---
title: "feat: Convert HA add-on config to YAML with nested MQTT configuration"
type: feat
date: 2026-06-15
---

## Summary

Convert the Home Assistant add-on configuration from JSON to YAML format, restructure MQTT parameters into a nested `mqtt_optional_config` dictionary with a single YAML CLI flag, and remove top-level `forecast_horizon`/`consumption_horizon` from the HA config schema.

## Problem Frame

The HA add-on currently uses `config.json`, but Home Assistant now standardizes on YAML for add-on configuration (`config.yaml`). The MQTT configuration surface exposes seven individual top-level parameters next to `mqtt_enabled`, creating visual noise in both the HA Supervisor UI and the CLI flag set. The top-level `forecast_horizon` and `consumption_horizon` keys only serve the deprecated `scheduler_mode=crontab` path and should be removed from the HA config to reduce user-facing complexity; the Go application defaults already match the current `config.json` defaults.

This work restructures the configuration surface without changing runtime behavior: the same MQTT defaults are preserved, the same validation rules apply, and the Go application's internal defaults for forecast/consumption horizons remain unchanged.

## Requirements

- R1. The HA add-on configuration file is `config.yaml` (YAML format), replacing `config.json`.
- R2. The YAML config carries the same add-on metadata (name, version, slug, arch, image, etc.) and the same schema types for options validation (`str`, `bool`, `int`, `float`, `password`, `match()`, `list()`).
- R3. `mqtt_enabled` remains a flat top-level boolean in both the HA config and the CLI.
- R4. All other MQTT parameters exposed to HA (`broker`, `client_id`, `username`, `password`, `topic_prefix`, `ha_discovery`, `ha_discovery_prefix`) are nested under `mqtt_optional_config` in the HA config, standalone `config.yaml`, and CLI flags.
- R5. The CLI exposes exactly `--mqtt_enabled` (bool) and `--mqtt_optional_config` (YAML string) — no individual per-field MQTT flags.
- R6. Defaults for `mqtt_optional_config` fields match today's `config.json` defaults: `topic_prefix: "sbam"`, `ha_discovery: true`, `ha_discovery_prefix: "homeassistant"`, all string fields empty.
- R7. TLS MQTT fields (`tls_ca_file`, `tls_client_cert`, `tls_client_cert_key`, `tls_insecure_skip`) are not part of `mqtt_optional_config` — they remain as separate CLI flags and env vars, excluded from the HA config schema as they are today.
- R8. Top-level `forecast_horizon` and `consumption_horizon` are removed from the HA add-on `options` and `schema`. Per-window `forecast_horizon` and `consumption_horizon` remain in the windows sub-schema.
- R9. The Go application's internal defaults for `forecast_horizon` (`"default"`) and `consumption_horizon` (`"full_day"`) are unchanged.
- R10. The `run.sh` bridge script is updated for the YAML config file and nested MQTT key access.
- R11. The release workflow reads the version from `config.yaml` instead of `config.json`.
- R12. The `config_schema_test.go` test reads and validates the YAML config file.
- R13. Credential redaction in `src/utils/startup.go` (`SecretKeys`) covers the nested `mqtt_optional_config.password` key.

## Key Technical Decisions

- **KTD-1. Use `yaml.Unmarshal` manual resolution for `--mqtt_optional_config` (same pattern as `--windows`).** The `--windows` flag already uses three-tier manual resolution (flag > env > config file). Replicating this pattern for `--mqtt_optional_config` keeps the codebase consistent and avoids type conflicts between a string flag value and a nested map from the config file.

- **KTD-2. Add `viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))` in `root.go`.** This enables individual env var overrides of nested keys — e.g., `MQTT_OPTIONAL_CONFIG_BROKER` overrides `mqtt_optional_config.broker` from the config file. The replacer is a no-op on flat keys (no dots), so existing env var behavior is unaffected. This was identified as a deferred need in issue #68.

- **KTD-3. No backward-compatibility fallback for flat MQTT env vars.** Old env var names (`MQTT_BROKER`, `MQTT_USERNAME`, `MQTT_PASSWORD`) will not work after this change. Users must switch to `MQTT_OPTIONAL_CONFIG_BROKER` etc., or set the whole block via `MQTT_OPTIONAL_CONFIG` as a YAML string. The HA add-on's `run.sh` auto-fill exports the new names, so HA users are unaffected. Standalone/Docker users with custom MQTT env vars will need to update their configuration.

- **KTD-4. Keep per-window `forecast_horizon` and `consumption_horizon` in the HA config.** These serve the `scheduler_mode=windows` path, which is the future direction. Only the top-level (crontab-serving) entries are removed.

- **KTD-5. Release workflow uses `yq` to extract the version from `config.yaml`.** `jq` cannot parse YAML. Adding `yq` (already available on `ubuntu-latest` runners via `mikefarah/yq`) to the release workflow is a one-line change.

## Implementation Units

### U1. Convert HA add-on config.json to config.yaml

**Goal:** Replace `home-assistant/addons/sbam/config.json` with an equivalent `config.yaml`.

**Requirements:** R1, R2

**Dependencies:** None

**Files:**
- `home-assistant/addons/sbam/config.json` — delete
- `home-assistant/addons/sbam/config.yaml` — create

**Approach:** Mechanical translation from JSON to YAML syntax. All add-on metadata fields (name, version, slug, init, description, services, arch, url, image) carry over unchanged. The `options` and `schema` blocks use the same type strings (`str`, `bool`, `int`, `float`, `password`, `match()`, `list()`). Nested structures (windows array-of-objects) use YAML indentation. This unit does NOT restructure MQTT keys or remove forecast_horizon/consumption_horizon — those changes happen in U2 and U4 respectively.

**Patterns to follow:** The current `config.json` structure; HA YAML add-on format from `https://developers.home-assistant.io/docs/apps/configuration`.

**Test scenarios:**
- Parse `config.yaml` with a YAML parser and verify all metadata fields match the current `config.json` values (name, version, slug, arch list, image template).
- Verify all schema type strings from `config.json` appear in `config.yaml` at the same paths.
- Verify nested structures (windows array schema) survive the translation intact.
- Covers `config_schema_test.go` adaptation: read `config.yaml` instead of `config.json`.

**Verification:** The YAML file parses without errors. All schema types present. All metadata fields match.

---

### U2. Restructure MQTT config in HA add-on config.yaml

**Goal:** Nest MQTT parameters under `mqtt_optional_config` in the HA add-on config, keeping `mqtt_enabled` flat. Update `run.sh` for nested key access and YAML options file.

**Requirements:** R3, R4, R6, R7, R10

**Dependencies:** U1

**Files:**
- `home-assistant/addons/sbam/config.yaml` — restructure `options` and `schema` MQTT sections
- `home-assistant/addons/sbam/run.sh` — update cp source, auto-fill function

**Approach:**

In `config.yaml`, replace the seven individual MQTT top-level keys with a single `mqtt_optional_config` dictionary in both `options` and `schema`:

```yaml
options:
  mqtt_enabled: false
  mqtt_optional_config:
    broker: ""
    client_id: ""
    username: ""
    password: ""
    topic_prefix: "sbam"
    ha_discovery: true
    ha_discovery_prefix: "homeassistant"

schema:
  mqtt_enabled: "bool"
  mqtt_optional_config:
    broker: "str?"
    client_id: "str?"
    username: "str?"
    password: "password?"
    topic_prefix: "str"
    ha_discovery: "bool"
    ha_discovery_prefix: "str"
```

In `run.sh`:
- Change `cp /data/options.json config.yaml` to `cp /data/options.yaml config.yaml` (HA Supervisor provides `options.yaml` for YAML-format add-on configs).
- Update `mqtt_autofill_from_ha_service()` to read nested keys via `bashio::config 'mqtt_optional_config.broker'` etc.
- Export `MQTT_OPTIONAL_CONFIG_BROKER` instead of `MQTT_BROKER`, `MQTT_OPTIONAL_CONFIG_USERNAME` instead of `MQTT_USERNAME`, `MQTT_OPTIONAL_CONFIG_PASSWORD` instead of `MQTT_PASSWORD`.

The TLS fields (`tls_ca_file`, `tls_client_cert`, `tls_client_cert_key`, `tls_insecure_skip`) remain absent from the HA config schema — the same exclusion as today.

**Patterns to follow:** The current `mqtt_autofill_from_ha_service()` function structure; the nested dict schema pattern from HA docs.

**Test scenarios:**
- Verify `bashio::config 'mqtt_optional_config.broker'` returns the nested value in a test HA environment.
- Verify auto-fill exports `MQTT_OPTIONAL_CONFIG_BROKER` when `mqtt_enabled=true` and the nested broker field is empty.
- Verify auto-fill skips when the nested broker field is already populated.
- Verify auto-fill skips when `mqtt_enabled=false`.
- Verify the `cp` source path references `options.yaml`.

**Verification:** The HA add-on starts successfully in a test environment with MQTT auto-fill populating credentials from the HA MQTT service.

---

### U3. Update Go-side config loading for nested MQTT keys and new CLI flag

**Goal:** Replace individual MQTT CLI flags with `--mqtt_optional_config`, update viper reads for nested keys, add `SetEnvKeyReplacer`, update `SecretKeys`, and wire the resolved config into `mqtt.Config`.

**Requirements:** R3, R4, R5, R6, R7, R13

**Dependencies:** U1, U2

**Files:**
- `pkg/cmd/root.go` — add `viper.SetEnvKeyReplacer`
- `pkg/cmd/schedule.go` — replace individual MQTT flags with `--mqtt_optional_config`, add manual resolution block, update `mqtt.Config` construction
- `src/utils/startup.go` — add `"mqtt_optional_config.password"` to `SecretKeys`

**Approach:**

In `root.go`, add after `viper.AutomaticEnv()`:
```go
viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
```

In `schedule.go`, introduce a private struct:
```go
type mqttOptionalConfig struct {
    Broker            string `yaml:"broker" mapstructure:"broker"`
    ClientID          string `yaml:"client_id" mapstructure:"client_id"`
    Username          string `yaml:"username" mapstructure:"username"`
    Password          string `yaml:"password" mapstructure:"password"`
    TopicPrefix       string `yaml:"topic_prefix" mapstructure:"topic_prefix"`
    HADiscovery       bool   `yaml:"ha_discovery" mapstructure:"ha_discovery"`
    HADiscoveryPrefix string `yaml:"ha_discovery_prefix" mapstructure:"ha_discovery_prefix"`
}
```

Replace the seven individual `--mqtt_*` flag registrations with:
```go
scdCmd.Flags().String("mqtt_optional_config", "", "MQTT optional config in YAML format")
```

In the `Run` function, resolve `mqtt_optional_config` with the same three-tier manual precedence used by `--windows` (flag > env > config file). Apply defaults for any fields not present in the resolved YAML. Construct `mqtt.Config` from the resolved struct, `mqtt_enabled`, and the separate TLS fields.

Remove the now-unused individual MQTT package-level variables (`mqtt_broker`, `mqtt_client_id`, `mqtt_username`, `mqtt_password`, `mqtt_topic_prefix`, `mqtt_ha_discovery`, `mqtt_ha_discovery_prefix`). The TLS variables stay.

In `startup.go`, append to `SecretKeys`:
```go
"mqtt_optional_config.password": {},
```

**Patterns to follow:** The `--windows` manual resolution block (`schedule.go` lines 124-141); the existing `mqtt.Config` construction (`schedule.go` lines 167-181).

**Test scenarios:**
- `--mqtt_optional_config` flag with valid YAML populates `mqtt.Config` correctly.
- `--mqtt_optional_config` with invalid YAML logs an error and returns early.
- `MQTT_OPTIONAL_CONFIG` env var with valid YAML populates config when flag is absent.
- `mqtt_optional_config` block in `config.yaml` populates config when neither flag nor env var is set.
- Defaults apply for fields omitted from the YAML input (topic_prefix → `"sbam"`, ha_discovery → `true`, ha_discovery_prefix → `"homeassistant"`).
- `MQTT_OPTIONAL_CONFIG_BROKER` env var overrides `mqtt_optional_config.broker` from config file (SetEnvKeyReplacer).
- `--mqtt_enabled=true` with empty `mqtt_optional_config` block — all string fields empty, bool fields at defaults, no error.
- TLS fields remain accessible via their separate flags and are not affected by `mqtt_optional_config`.
- SecretKeys redacts `mqtt_optional_config.password` in startup debug dump.
- Flag > env > config file precedence is verified for the `mqtt_optional_config` block.

**Verification:** `make test` passes. A `config.yaml` with nested `mqtt_optional_config` produces the same `mqtt.Config` as the current flat-key config.yaml.

---

### U4. Remove forecast_horizon and consumption_horizon from HA config

**Goal:** Remove top-level `forecast_horizon` and `consumption_horizon` entries from the HA add-on `options` and `schema`. Keep per-window entries.

**Requirements:** R8, R9

**Dependencies:** U1

**Files:**
- `home-assistant/addons/sbam/config.yaml` — remove top-level entries from `options` and `schema`

**Approach:** Delete the `forecast_horizon` and `consumption_horizon` lines from both the `options` and `schema` blocks in `config.yaml`. The per-window entries inside the `windows` array schema are preserved. The Go application continues to use its internal constants (`"default"` and `"full_day"`) when these keys are absent from the config file — no Go-side changes needed.

**Patterns to follow:** The existing windows sub-schema entries for `forecast_horizon` and `consumption_horizon`.

**Test scenarios:**
- Verify the HA config YAML parses without `forecast_horizon` or `consumption_horizon` at the top level of `options` and `schema`.
- Verify the per-window `forecast_horizon` and `consumption_horizon` entries remain in the windows sub-schema.
- Verify the Go application starts with the new config file and uses the internal defaults (`"default"` / `"full_day"`).

**Verification:** The HA Supervisor UI no longer renders forecast_horizon or consumption_horizon fields at the top level. The windows configuration UI still shows per-window horizon overrides.

---

### U5. Update tests for YAML config, nested MQTT keys, and new flag

**Goal:** Update all tests affected by the config format change, MQTT key restructuring, and new flag shape.

**Requirements:** R12

**Dependencies:** U1, U2, U3, U4

**Files:**
- `pkg/cmd/config_schema_test.go` — read YAML instead of JSON
- `pkg/cmd/schedule_test.go` — update MQTT flag references, config access
- `pkg/cmd/precedence_test.go` — add nested-key precedence cases, update MQTT key tests
- `pkg/cmd/schedule_mqtt_wiring_test.go` — update MQTT config construction
- `pkg/cmd/schedule_lifecycle_test.go` — update MQTT config construction
- `pkg/cmd/schedule_cron_test.go` — update MQTT config construction
- `src/utils/startup_test.go` — verify nested key redaction

**Approach:**

`config_schema_test.go`: Change from `os.ReadFile("../../home-assistant/addons/sbam/config.json")` + `json.Unmarshal` to reading `config.yaml` with `gopkg.in/yaml.v3`. The test struct uses `yaml:` tags instead of `json:` tags. The crontab regex extraction logic adapts to the YAML schema format (which uses the same `match(...)` wrapper).

Precedence tests: The existing `TestBindFlags_RealScheduleCmdMQTTKeysPrecedence` tests each MQTT key through all four precedence levels. Replace these with tests for the `mqtt_optional_config` block precedence and individual `mqtt_enabled` precedence. Add tests for `SetEnvKeyReplacer` behavior with nested keys (`MQTT_OPTIONAL_CONFIG_BROKER` overriding `mqtt_optional_config.broker` from config file).

Other schedule test files: Any test that constructs `mqtt.Config` from individual variables updates to use the resolved `mqttOptionalConfig` struct. Tests that set MQTT env vars update to use the new dotted env var names.

**Patterns to follow:** The existing `TestCrontabSchemaRegex` test structure; the existing precedence test helpers (`resetViper`, `writeConfig`, `newFlagCmd`); the `TestBindFlags_RealScheduleCmdMQTTKeysPrecedence` coverage pattern.

**Test scenarios:**
- `TestCrontabSchemaRegex` reads `config.yaml` and validates the crontab regex (same pass/fail cases as today).
- Precedence test: `mqtt_optional_config` flag beats env and config file.
- Precedence test: `MQTT_OPTIONAL_CONFIG` env var beats config file.
- Precedence test: config file `mqtt_optional_config` block beats internal defaults.
- Precedence test: `MQTT_OPTIONAL_CONFIG_BROKER` env var overrides `mqtt_optional_config.broker` from config file (SetEnvKeyReplacer).
- Precedence test: `mqtt_enabled` flag path unchanged, still follows flag > env > config > default.
- Schedule test: MQTT config populated from `--mqtt_optional_config` flag produces correct `mqtt.Config`.
- Schedule test: MQTT config populated from config file with nested block produces correct `mqtt.Config`.
- Schedule test: defaults applied when `mqtt_optional_config` is empty/absent.
- Startup test: `mqtt_optional_config.password` is redacted in debug dump.

**Verification:** `make test` passes with all updated tests. No test regressions.

---

### U6. Update release workflow for config.yaml

**Goal:** Update the release workflow to read the version from `config.yaml` instead of `config.json`.

**Requirements:** R11

**Dependencies:** U1

**Files:**
- `.github/workflows/release.yml` — change `jq` invocation to `yq`

**Approach:** In `release.yml`, change line 29 from:
```bash
VERSION_FROM_FILE=$(jq -r '.version' home-assistant/addons/sbam/config.json)
```
to:
```bash
VERSION_FROM_FILE=$(yq '.version' home-assistant/addons/sbam/config.yaml)
```

`yq` (mikefarah/yq) is pre-installed on `ubuntu-latest` GitHub runners.

**Patterns to follow:** The existing version extraction logic — only the tool and file path change.

**Test scenarios:**
- `yq '.version' home-assistant/addons/sbam/config.yaml` returns the version string from the new YAML file.
- The version comparison logic (stable vs. pre-release) works identically with the yq output.

**Verification:** Running the version extraction command locally against `config.yaml` returns the expected version string. The release workflow's determine-release job produces correct `version` and `release` outputs.

---

## Scope Boundaries

### In scope
- Converting `config.json` to `config.yaml` (HA add-on config)
- Restructuring MQTT parameters into `mqtt_optional_config` with a single YAML CLI flag
- Updating `run.sh` for YAML options file and nested key access
- Adding `viper.SetEnvKeyReplacer` for nested-key env var support
- Updating `SecretKeys` for the nested password key
- Removing top-level `forecast_horizon`/`consumption_horizon` from HA config
- Updating all affected tests
- Updating the release workflow

### Out of scope
- Modifying the standalone `config.yaml` template (no template exists in the repo)
- Changes to MQTT discovery payloads or Go-level MQTT publish/subscribe behavior
- Changes to the Dockerfile or standalone Docker image
- Adding or modifying HA add-on services or dependencies
- Removing `forecast_horizon`/`consumption_horizon` CLI flags or Go-side validation (these remain for standalone users)
- TLS MQTT field restructuring (they stay as separate flat flags)

### Deferred to Follow-Up Work
- Backward-compatibility fallback: reading old flat MQTT env vars (`MQTT_BROKER`) as a fallback when `MQTT_OPTIONAL_CONFIG_BROKER` is not set — would ease migration for standalone/Docker users
- HA add-on migration helper: a `run.sh` pre-start hook that detects old flat MQTT keys in `/data/options.yaml` and migrates them into the nested structure
- Documentation updates: `docs/mqtt.md` MQTT configuration section should reflect the new env var names and nested config structure

## Risks & Dependencies

- **Breaking env var rename.** Users who set `MQTT_BROKER`, `MQTT_USERNAME`, or `MQTT_PASSWORD` env vars directly (standalone CLI, Docker) must update to `MQTT_OPTIONAL_CONFIG_BROKER`, `MQTT_OPTIONAL_CONFIG_USERNAME`, `MQTT_OPTIONAL_CONFIG_PASSWORD`. The old names will be silently ignored. HA add-on users are unaffected because `run.sh` auto-fill exports the new names.
- **HA add-on upgrade loses MQTT config.** When existing HA users upgrade the add-on, the Supervisor retains old flat MQTT keys in `/data/options.yaml` but the new schema nests them under `mqtt_optional_config`. The Supervisor will show empty MQTT fields, and the user must re-enter them. A CHANGELOG entry and release notes should call this out explicitly.
- **`bashio::config` nested access.** The `bashio::config 'mqtt_optional_config.broker'` dotted notation must be verified against the installed `bashio` version in the HA base image. If the base image ships an older bashio that does not support dotted key access, the auto-fill function will need to parse the JSON options file directly (e.g., with `jq`).
- **`yq` availability in CI.** The release workflow relies on `yq` being pre-installed on `ubuntu-latest`. This is the case today, but if GitHub changes the runner image, the release workflow will fail. A fallback could use a Go-based YAML reader or `grep`/`sed` on the simple `version:` line.

## Sources / Research

- Home Assistant add-on configuration docs: `https://developers.home-assistant.io/docs/apps/configuration` — confirms YAML support, schema type compatibility, nested dict support up to depth 2
- Current `home-assistant/addons/sbam/config.json` — source of truth for all schema types, defaults, and metadata
- `pkg/cmd/schedule.go` lines 124-141 — the `--windows` manual resolution pattern to replicate for `--mqtt_optional_config`
- `pkg/cmd/root.go` line 42 — current `viper.AutomaticEnv()` call site for `SetEnvKeyReplacer` addition
- `src/utils/startup.go` lines 32-36 — `SecretKeys` registry to extend
- `home-assistant/addons/sbam/run.sh` lines 6, 14-36 — cp bridge and MQTT auto-fill function to update
- `.github/workflows/release.yml` line 29 — `jq` version extraction to replace with `yq`
- Issue #68 plan (`docs/implementations/archive/68-issue-cli-flags-precedence/`) — deferred `SetEnvKeyReplacer` need now actioned
- Issue #146 plan (`docs/implementations/archive/146-issue-multi-window-charging/`) — established the `cp /data/options.json config.yaml` bridge pattern
