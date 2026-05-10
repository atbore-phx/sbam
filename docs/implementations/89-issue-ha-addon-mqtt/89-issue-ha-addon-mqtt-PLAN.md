# PLAN: Home Assistant add-on MQTT finish

> Feature slug: `89-issue-ha-addon-mqtt`  
> TASK: [89-issue-ha-addon-mqtt-TASK.md](89-issue-ha-addon-mqtt-TASK.md)  
> Issue: https://github.com/atbore-phx/sbam/issues/89  
> Parent issue: https://github.com/atbore-phx/sbam/issues/64  
> Reconciled: 2026-05-10

## 1. Current State

`home-assistant/addons/sbam/config.json` already has version `2.0.0` and non-TLS MQTT options. `run.sh` exports those options. `DOCS.md` documents MQTT but contains duplicated option bullets. `CHANGELOG.md` has a `2.0.0` entry.

Missing: `services: ["mqtt:need"]`, Mosquitto auto-discovery in `run.sh`, and DOCS cleanup.

## 2. Implementation Steps

1. Add `services: ["mqtt:need"]` near the top-level add-on config fields.
2. Keep the existing non-TLS MQTT options/schema. Do not add TLS UI fields unless a separate issue asks for them.
3. In `run.sh`, after reading user options and before starting `sbam`, check:
   - `MQTT_ENABLED=true`
   - `MQTT_BROKER` is empty
   - `bashio::services.available 'mqtt'`
4. If available, set `MQTT_BROKER=tcp://<host>:<port>` and fill username/password only when empty.
5. Clean `DOCS.md` so MQTT options appear once and include Mosquitto auto-fill behavior.
6. Update `CHANGELOG.md` only if the existing `2.0.0` entry needs the service auto-discovery note.

## 3. Test Plan

- Static check: `config.json` parses and contains `services` plus password schema for `mqtt_password`.
- Shell review/test: run.sh preserves manual broker values and only fills empty ones from bashio services.
- Docs check: MQTT option bullets are not duplicated.
- Add-on build: run `home-assistant/addons/test_local.sh` when the environment supports Docker/buildx.

## 4. Validation

```bash
home-assistant/addons/test_local.sh
```

If Docker or HA build tooling is unavailable, document that limitation in the PR notes and run JSON/shell-focused checks instead.
