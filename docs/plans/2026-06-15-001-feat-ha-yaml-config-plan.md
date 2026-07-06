---
title: "feat: Convert HA add-on config to YAML, remove deprecated horizon fields"
type: feat
date: 2026-06-15
---

## Summary

Convert the Home Assistant add-on configuration from JSON to YAML format and remove top-level `forecast_horizon`/`consumption_horizon` from the HA config schema. MQTT configuration is unchanged — flat keys, individual CLI flags, and the existing `run.sh` auto-fill are preserved as-is.

## Problem Frame

The HA add-on currently uses `config.json`, but Home Assistant now supports YAML for add-on configuration (`config.yaml`) with identical schema types. The top-level `forecast_horizon` and `consumption_horizon` keys only serve the deprecated `scheduler_mode=crontab` path and should be removed from the HA config to reduce user-facing complexity; the Go application defaults already match the current `config.json` defaults.

The MQTT configuration surface, CLI flags, env vars, and `run.sh` auto-fill are left untouched — the original restructuring was reverted after deployment testing revealed integration issues with the HA MQTT service auto-fill.

## Requirements

- R1. The HA add-on configuration file is `config.yaml` (YAML format), replacing `config.json`.
- R2. The YAML config carries the same add-on metadata (name, version, slug, arch, image, etc.) and the same schema types for options validation (`str`, `bool`, `int`, `float`, `password`, `match()`, `list()`).
- R3. All MQTT configuration keys remain flat top-level entries in the HA config, CLI flags, and standalone `config.yaml` — no nesting, no new flags, no env var renames.
- R4. Top-level `forecast_horizon` and `consumption_horizon` are removed from the HA add-on `options` and `schema`. Per-window `forecast_horizon` and `consumption_horizon` remain in the windows sub-schema.
- R5. The Go application's internal defaults for `forecast_horizon` (`"default"`) and `consumption_horizon` (`"full_day"`) are unchanged.
- R6. The release workflow reads the version from `config.yaml` instead of `config.json`.
- R7. The `config_schema_test.go` test reads and validates the YAML config file.
- R8. `run.sh` is reverted to its pre-branch state — reading flat `bashio::config` keys and exporting flat `MQTT_BROKER`, `MQTT_USERNAME`, `MQTT_PASSWORD` env vars.
- R9. Go-side MQTT config loading is reverted to its pre-branch state — individual `--mqtt_*` flags, flat viper keys, no `mqttOptionalConfig` struct, no `SetEnvKeyReplacer`.

## Key Technical Decisions

- **KTD-1. Keep per-window `forecast_horizon` and `consumption_horizon` in the HA config.** These serve the `scheduler_mode=windows` path, which is the future direction. Only the top-level (crontab-serving) entries are removed.

- **KTD-2. Release workflow uses `yq` to extract the version from `config.yaml`.** `jq` cannot parse YAML. `yq` (mikefarah/yq) is pre-installed on `ubuntu-latest` GitHub runners.

- **KTD-3. MQTT configuration surface is unchanged.** The nested `mqtt_optional_config` approach was reverted — flat keys, individual flags, and flat env vars are the stable interface. The HA add-on config YAML keeps MQTT keys at the top level of `options` and `schema`, matching the pre-branch JSON structure.

## Implementation Units

### U1. Revert MQTT changes in Go code

**Goal:** Remove all MQTT restructuring from `pkg/cmd/schedule.go`, `pkg/cmd/root.go`, `src/utils/startup.go`, restoring the pre-branch flat-key state.

**Requirements:** R3, R9

**Dependencies:** None

**Files:**
- `pkg/cmd/schedule.go` — remove `mqttOptionalConfig` struct and `mqttOptsDefaults`, restore individual MQTT package-level vars and flag registrations, restore flat `viper.Get*` calls and flat `mqtt.Config` construction
- `pkg/cmd/root.go` — remove `viper.SetEnvKeyReplacer` and `"strings"` import
- `src/utils/startup.go` — remove `"mqtt_optional_config.password"` from `SecretKeys`

**Approach:** Use `git diff <pre-branch-commit> -- pkg/cmd/schedule.go pkg/cmd/root.go src/utils/startup.go` to identify the exact pre-branch state and revert mechanically. The config.yaml conversion and horizon removal changes to these files (if any) are preserved — only MQTT-related additions are reverted.

**Patterns to follow:** The pre-branch flat MQTT key structure; individual `--mqtt_*` flag registrations; flat `viper.GetString`/`viper.GetBool` calls.

**Test scenarios:**
- `make build` compiles without errors after revert.
- Individual `--mqtt_broker`, `--mqtt_username`, `--mqtt_password`, `--mqtt_topic_prefix`, `--mqtt_ha_discovery`, `--mqtt_ha_discovery_prefix`, `--mqtt_client_id` flags are registered and functional.
- `mqtt.Config` is constructed from individual flat variables.
- `SecretKeys` contains `"mqtt_password"` but not `"mqtt_optional_config.password"`.
- `viper.SetEnvKeyReplacer` is absent from `root.go`.

**Verification:** `make build` succeeds. Grep confirms no `mqttOptionalConfig`, `SetEnvKeyReplacer`, or `mqtt_optional_config` in Go source outside of comments.

---

### U2. Revert run.sh to pre-branch state

**Goal:** Restore `run.sh` to its pre-branch MQTT auto-fill behavior — flat `bashio::config` keys and flat env var exports.

**Requirements:** R8

**Dependencies:** U1

**Files:**
- `home-assistant/addons/sbam/run.sh` — revert to pre-branch content

**Approach:** Check out the pre-branch version of `run.sh` from git. The only change from pre-branch that must be preserved is the `config.json` → `config.yaml` file existence (the `cp` source path is already `/data/options.json` in the pre-branch version — see note below). Verify the pre-branch `run.sh` works correctly with the new `config.yaml`.

**Note:** The pre-branch `run.sh` already copies `/data/options.json` to `config.yaml`, so the YAML conversion requires no `run.sh` changes.

**Patterns to follow:** The pre-branch `run.sh` from the commit before any MQTT restructuring changes.

**Test scenarios:**
- `run.sh` reads `mqtt_enabled`, `mqtt_broker`, `mqtt_username`, `mqtt_password` via flat `bashio::config` calls.
- `run.sh` exports `MQTT_BROKER`, `MQTT_USERNAME`, `MQTT_PASSWORD` (flat names).
- No `mqtt_optional_config` references in `run.sh`.
- No `jq` usage in `run.sh`.

**Verification:** Diff against the pre-branch `run.sh` shows zero differences (or only the preserved `config.yaml` path if the pre-branch version differed).

---

### U3. Flatten MQTT keys in HA add-on config.yaml

**Goal:** Restore flat MQTT keys in `config.yaml`'s `options` and `schema` blocks, removing the `mqtt_optional_config` nesting.

**Requirements:** R3

**Dependencies:** None

**Files:**
- `home-assistant/addons/sbam/config.yaml` — replace nested `mqtt_optional_config` block with individual flat MQTT keys

**Approach:** In the `options` block, replace:
```yaml
mqtt_optional_config:
  broker: ""
  ...
```
with:
```yaml
mqtt_broker: ""
mqtt_client_id: ""
mqtt_username: ""
mqtt_password: ""
mqtt_topic_prefix: sbam
mqtt_ha_discovery: true
mqtt_ha_discovery_prefix: homeassistant
```
Same for the `schema` block. `mqtt_enabled` stays as-is (it was never nested).

**Patterns to follow:** The original `config.json` flat MQTT key structure.

**Test scenarios:**
- Parse `config.yaml` and verify flat MQTT keys (`mqtt_broker`, `mqtt_username`, etc.) at the top level of `options` and `schema`.
- Verify `mqtt_optional_config` key is absent from both `options` and `schema`.
- Verify `mqtt_enabled` remains a flat top-level boolean.

**Verification:** `config.yaml` parses without errors. All MQTT keys are flat at the top level, matching the original `config.json` structure.

---

### U4. Update tests for YAML config, horizon removal, and MQTT revert

**Goal:** Update all tests to reflect: YAML config format, removed horizon fields, and reverted MQTT keys.

**Requirements:** R7

**Dependencies:** U1, U2, U3

**Files:**
- `pkg/cmd/config_schema_test.go` — already reads `config.yaml` from prior work; verify it still passes
- `pkg/cmd/precedence_test.go` — restore original flat MQTT key test cases, remove `mqtt_optional_config` block test
- `src/utils/startup_test.go` — restore original `mqtt_password` flag-based redaction test

**Approach:** Restore the pre-branch MQTT test cases for `precedence_test.go` and `startup_test.go`. The `config_schema_test.go` YAML-parsing change from the original plan is preserved (it reads `config.yaml` instead of `config.json`).

**Patterns to follow:** The pre-branch `TestBindFlags_RealScheduleCmdMQTTKeysPrecedence` and `TestDumpStartupParams_RedactsMQTTSecrets`.

**Test scenarios:**
- `TestCrontabSchemaRegex` reads `config.yaml` and validates the crontab regex.
- `TestBindFlags_RealScheduleCmdMQTTKeysPrecedence` tests all flat MQTT string keys (mqtt_broker, mqtt_client_id, mqtt_username, mqtt_password, mqtt_tls_ca_file, mqtt_tls_client_cert, mqtt_tls_client_cert_key, mqtt_topic_prefix, mqtt_ha_discovery_prefix) through flag/env/yaml/default precedence.
- `TestBindFlags_RealScheduleCmdMQTTKeysPrecedence` tests all flat MQTT bool keys (mqtt_enabled, mqtt_tls_insecure_skip, mqtt_ha_discovery) through flag/env/yaml/default precedence.
- `TestDumpStartupParams_RedactsMQTTSecrets` redacts `mqtt_password` set via flag.
- No test references `mqtt_optional_config`, `mqttOptionalConfig`, or `SetEnvKeyReplacer`.

**Verification:** `make test` passes with all tests updated. No MQTT-related test regressions.

## Scope Boundaries

### In scope
- Reverting MQTT restructuring in Go code, run.sh, and HA config.yaml
- Preserving JSON→YAML conversion, horizon field removal, and release workflow update

### Out of scope
- Any new MQTT features or restructuring
- Changes to MQTT discovery payloads or Go-level publish/subscribe behavior
- Standalone config.yaml template
- Documentation updates

## Risks & Dependencies

- **No breaking changes.** Unlike the original plan, this revision introduces zero user-facing changes to MQTT configuration. Flat keys, individual flags, and flat env vars are the stable interface.
- **YAML conversion is low-risk.** The HA Supervisor supports both JSON and YAML add-on config formats with identical schema types. The release workflow's `yq` dependency is pre-installed on CI runners.
