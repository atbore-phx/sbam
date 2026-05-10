# Feature: MQTT docs and migration note

> Source issue: [#91](https://github.com/atbore-phx/sbam/issues/91)  
> Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)  
> Reconciled: 2026-05-10  
> Slug: `91-issue-docs-mqtt`

## Summary

Finish release documentation for the v2.0.0 MQTT feed. The README and project structure already contain some MQTT text from #85, but they need a complete topic map, state/ack payload schemas, command examples, Home Assistant notes, and a migration callout.

## Scope

- Expand the README MQTT section.
- Document all runtime topics: availability, state, error, `cmd/<name>`, `cmd/<name>/ack`, and HA discovery config topics.
- Document `StatePayload` fields as implemented by `pkg/mqtt` and `schedule`.
- Document the #86 ack shape: `ts`, `command`, `accepted`, optional `error`.
- Add `mosquitto_pub` examples for `trigger_now`, `pause`, `resume`, `force_charge`, and `set_defaults`.
- Note that `set_reserve` is deferred beyond v2.0.0.
- Add migration note: v1.x users need no changes while `mqtt_enabled=false`.
- Update `.github/copilot-instructions.md` only for new source/test files added by #87/#88.

Out of scope:

- Changing source behavior.
- Home Assistant add-on DOCS cleanup (#89).

## Acceptance Criteria

- [ ] README topic map matches `pkg/mqtt` helper topics.
- [ ] README state payload example matches `mqtt.StatePayload` JSON fields.
- [ ] README ack example matches `mqtt.AckPayload` JSON fields.
- [ ] Command examples use implemented command names only.
- [ ] Migration note explains the opt-in default and twelve standalone MQTT keys.
- [ ] `.github/copilot-instructions.md` Project Structure includes any runner files created by #87/#88.
