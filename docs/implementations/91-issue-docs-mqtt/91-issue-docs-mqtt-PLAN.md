# PLAN: MQTT docs and migration note

> Feature slug: `91-issue-docs-mqtt`  
> TASK: [91-issue-docs-mqtt-TASK.md](91-issue-docs-mqtt-TASK.md)  
> Issue: https://github.com/atbore-phx/sbam/issues/91  
> Parent issue: https://github.com/atbore-phx/sbam/issues/64  
> Reconciled: 2026-05-10

## 1. Current State

`README.md` has a short MQTT/Home Assistant discovery section and the schedule help lists MQTT flags. `.github/copilot-instructions.md` already lists `pkg/mqtt` source/test files, but runner files do not exist yet.

Missing: complete topic map, payload schemas, command examples, HA discovery explanation, and migration callout.

## 2. Implementation Steps

1. Expand README with an `MQTT feed` section or enrich the existing section.
2. Add enablement examples for CLI flags, env vars, and YAML.
3. Add topic map:
   - `<prefix>/availability`
   - `<prefix>/state`
   - `<prefix>/error`
   - `<prefix>/cmd/<name>`
   - `<prefix>/cmd/<name>/ack`
   - `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config`
4. Add JSON examples for state and ack payloads.
5. Add command examples with `mosquitto_pub` for all implemented v2.0 commands.
6. Add HA Discovery note: retained discovery payloads, device grouping, and `homeassistant/status=online` re-publication.
7. Add migration note from v1.x and list the twelve standalone MQTT keys.
8. Update `.github/copilot-instructions.md` Project Structure after #87/#88 create runner files.

## 3. Test Plan

- Cross-check README topic names against `pkg/mqtt/client.go`, `discovery.go`, and `commands.go`.
- Cross-check JSON examples against `mqtt.StatePayload` and `mqtt.AckPayload` structs.
- Render/preview README markdown for table and code block correctness.
- Run `make test` only if documentation changes are bundled with source changes.

## 4. Validation

No source validation is required for documentation-only changes, but the final PR should mention the cross-checks performed.
