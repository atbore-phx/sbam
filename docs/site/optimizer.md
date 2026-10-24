# Cost-optimized Dispatch

`scheduler_mode: optimize` replaces fixed charge windows with an hourly plan that minimizes the expected electricity cost. Every hour, sbam reads spot prices and household consumption history from Home Assistant, combines them with the Solcast forecast and the battery's state of charge, and decides what the battery should do in the current hour.

!!! warning "Dry run is the default"
    `opt_dry_run` defaults to `true` on every deployment target. In dry run, sbam computes and publishes the plan but writes nothing to the inverter. Keep it that way until you have compared the plan with reality for a few days and worked through the [hardware checklist](#before-going-live).

Existing `crontab` and `windows` installations are not affected. The optimizer runs only when `scheduler_mode` is `optimize`.

---

## How it works

### Receding horizon

- sbam runs the optimizer once at startup and then at every full hour (`xx:00:00`).
- Each run plans from the current hour to the last hour that has a price, capped at **36 hours**. Before tomorrow's prices are published (normally around 13:00), the horizon ends at midnight. After that, it reaches midnight tomorrow.
- Only the first hour of the plan is applied. The next hour, sbam plans again with fresh prices, SoC and forecast. This is called a receding horizon.
- The first hour is scaled to the time left in it, so a run at 14:20 plans 40 minutes for the 14:00 slot.
- Hours follow local time. On DST days the plan has 23 or 25 hourly slots, and each slot uses the surcharge for its own local hour.

### Inputs

| Input | Source | Used for |
|-------|--------|----------|
| Spot price per hour, incl. VAT | Nordpool sensor in Home Assistant (`opt_price_entity`). Quarter-hour prices are averaged per hour. | Buy and sell price |
| Household consumption profile | 10 days (`opt_history_days`) of history of the load power sensor (`opt_consumption_entity`), minus the EV charger (`opt_ev_entity`, optional) | Expected consumption per hour of day |
| PV forecast | Solcast, always through the forecast cache (`cache_file_prefix`, `cache_time`). After Solcast reports its daily limit as exceeded, sbam stops asking until the limit resets at 00:00 UTC and uses the cache; the log says so once at Info | Expected PV production per hour |
| Battery SoC and capacity | Fronius Solar API | Starting energy |
| Battery model | `opt_battery_efficiency`, `opt_max_charge_rate_w`, `opt_max_discharge_rate_w`, `opt_min_soc_pct` | Limits and losses |

The consumption profile is built once a day, after the first run of a new local day. An hour of day needs at least 3 days with usable history. Hours with less history use the static fallback `pw_consumption / 24` and add the reason `optimizer_consumption_static_fallback`.

### Buy and sell price

For every hour `h`:

```
buy[h]  = spot_incl_vat[h] + opt_purchase_surcharge[hour_of_day(h)]
sell[h] = spot_incl_vat[h] / (1 + opt_vat_rate) - opt_sell_margin
```

- The surcharges (grid tariff, taxes, supplier fee) are entered **incl. VAT**, one value per hour of the day.
- The sell price is the spot price without VAT, minus your supplier's margin. It may be negative.

**Example.** Spot 2.00 DKK/kWh incl. VAT at 18:00, surcharge for hour 18 = 2.04, `opt_vat_rate` = 0.25, `opt_sell_margin` = 0.05:
`buy = 2.00 + 2.04 = 4.04` and `sell = 2.00 / 1.25 - 0.05 = 1.55`.

### Efficiency

`opt_battery_efficiency` (η) is the round-trip efficiency. sbam splits the loss evenly between charging and discharging: storing ΔE Wh draws ΔE / √η Wh from the grid or PV, and delivering X Wh to the house or grid takes X / √η Wh from the battery. With η = 0.83 that is about 9 % each way, which matches what a Gen24 with a typical battery shows in the `plan check` log. Grid-charging still only pays off when a later price is higher than the current price divided by η: with η = 0.83, a buy price of 1.00 now only beats a later price above 1.20.

### Terminal value

Energy still in the battery at the end of the horizon is valued at `η × mean(buy prices in the horizon)` per kWh. Without that, a short horizon (before tomorrow's prices arrive) would make the plan empty the battery just because the horizon ends.

The expected cost reported by sbam is:

```
expected_cost = Σ (import_kWh × buy − export_kWh × sell) − terminal_value
```

It can be negative when the plan expects to earn more from export than it pays for import.

### Solver

The optimizer is a dynamic program over 200 SoC steps between `opt_min_soc_pct` and 100 %. It is deterministic: the same inputs always give the same plan. When two choices cost the same, it picks the least intrusive one, with `self_consume` first.

### Minimum gain and wear cost (`opt_min_gain`, `opt_wear_cost`)

A forced action is only worth a register write if it has a purpose. sbam therefore leaves the inverter in `self_consume` unless the forced action saves enough compared with self-consumption in that hour:

| Action | Must save at least |
|--------|--------------------|
| `charge`, `discharge` | `opt_min_gain` per hour + `opt_wear_cost` × the kWh moved into or out of the battery beyond what self-consumption would move |
| `hold`, `standby` | a tenth of `opt_min_gain` per hour |

`opt_min_gain` defaults to 0.50 per hour and `opt_wear_cost` to 0 (off); both are in the price sensor's currency. A wear cost of around 0.15 DKK/kWh is a reasonable start for a home battery.

- A discharge that would only export a few dozen Wh for a few øre stays `self_consume`, and the reason reads `optimizer_self_consume_no_gain`.
- Charging at night for an evening peak, which saves several DKK, still happens.
- `hold` and `standby` move no battery energy and cause no wear, so waiting is cheap: sbam charges in the cheapest hour and holds until the peak, rather than charging in a dearer hour just before it. Likewise it can export morning PV in `standby` when midday PV will fill the battery anyway. The tenth that remains only stops it switching for pennies.
- The wear cost grows with the energy moved: with 0.15/kWh, topping up 1 kWh needs 0.15 on top of `opt_min_gain`, moving 4 kWh needs 0.60.
- Both only steer decisions. The expected cost and terminal value sbam reports are the real model costs.

Raise them for fewer interventions, lower them to act on smaller price differences; `0` disables each.

### The five actions

| Action | What the battery does | Rate |
|--------|----------------------|------|
| `self_consume` | Inverter default behaviour: PV surplus charges, deficits discharge. Same as `sbam configure -d`. | 0 |
| `charge` | Charges from grid and PV at least at the given rate. | W |
| `hold` | Does not discharge. PV surplus may still charge it. No grid charging. | 0 |
| `standby` | Neither charges nor discharges. PV surplus is exported. Used to sell morning PV when later PV will fill the battery anyway. | 0 |
| `discharge` | Discharges at least at the given rate into the house and grid. The surplus is exported. | W |

Rates in the plan, on MQTT and in `rate_w` are **battery-side**: a `charge` rate is the power going into the battery, a `discharge` rate the power coming out of it. Charging 3.7 kWh in an hour is a 3 700 W rate; the grid supplies 3.7 / √0.83 ≈ 4.1 kWh for it, and only the cost uses that larger figure. `charge` and `discharge` rates are capped by `opt_max_charge_rate_w` / `opt_max_discharge_rate_w` (both battery-side) and by the inverter's `WChaMax`.

The Gen24 does not treat its two setpoints alike, so sbam converts the discharge setpoint before writing it:

- **Charge setpoint: battery-side.** The inverter stores what it is told; in the live trial the stored energy matched the setpoint within 1–2 %. sbam writes the planned rate.
- **Discharge setpoint: AC-side.** The inverter delivers the setpoint to the house and grid and takes the conversion loss from the battery on top: the battery drained 1.10 × the setpoint, which is 1 / √0.83. sbam therefore writes the planned battery-side rate × √η, for example 3 000 W planned → 2 733 W setpoint, so the battery delivers the 3 000 W the plan counts on. The `dispatch applied` and dry-run log lines show both `rate_w` (battery-side) and `setpoint_w` (written). For the current hour, the discharge rate is also clamped so it never plans below `opt_min_soc_pct` + 1 %. If that clamp brings the rate to 0, sbam writes `standby` instead, with reason `optimizer_discharge_clamped_min_soc`: like the planned discharge, it neither charges the battery nor keeps PV surplus from being exported (`hold` would let PV charge the battery, the opposite of what the plan wanted). The published plan still shows the planned `discharge`.

### EV guard (`opt_ev_guard_w`)

The consumption profile excludes the EV charger, so the plan never budgets battery energy for the car. The inverter cannot tell the car from the house: in a `self_consume` hour it covers the whole deficit, and a car drawing 3–11 kW empties the battery in a couple of hours, typically energy the plan kept for the evening peak.

With `opt_ev_entity` set, sbam reads the EV power every 60 seconds. When the car draws more than `opt_ev_guard_w` (default 1000 W) in an hour the plan runs in `self_consume`, sbam applies `hold` instead: the battery stops discharging, the house and the car are supplied from PV and the grid, and PV surplus still charges the battery. In summer, when PV covers house and car, nothing changes.

- The guard releases when the car draws less than half of `opt_ev_guard_w`, after it has been engaged for at least 5 minutes. sbam then writes the hour's planned action again. The `ev guard released` line carries `release_reason`: `ev_stopped` or `ev_power_unknown`.
- It only acts on `self_consume` hours. `charge`, `hold`, `standby` and `discharge` hours are not changed.
- While engaged, the state shows `opt_action: hold` and the reasons include `optimizer_ev_guard_hold`. The log has `ev guard engaged` / `ev guard released` lines, and the `plan check` line of a guarded hour carries `ev_guard: true`.
- If the EV sensor cannot be read (for example when the charger drops off Wi-Fi), the guard keeps its state. After 15 minutes without a valid reading it releases and the planned action applies again, so a lost sensor never keeps the battery held for hours. Short dropouts are logged at Debug; a Warn appears after three failed readings in a row, and an Info line when readings recover. A car that starts charging while the sensor is offline is not detected until the sensor is back.
- If a guard write fails, the defaults are restored and the guard waits for the next hourly run.
- Pause, `set_defaults` and `force_charge` release the guard until the next hourly run. In dry run the guard logs the registers it would write.
- `opt_ev_guard_w: 0` disables the guard.

---

## Setup

Check the [prerequisites](prerequisites.md#cost-optimizer-prerequisites) first: you need the Nordpool integration, a household load power sensor, enough recorder history, and access to the Home Assistant API.

### Home Assistant add-on

1. Open **Settings → Add-ons → sbam → Configuration**.
2. Set `scheduler_mode` to `optimize`.
3. Fill in `opt_price_entity` and `opt_consumption_entity`, and optionally `opt_ev_entity`.
4. Paste the 24 hourly surcharges into `opt_purchase_surcharge` as one line, for example `0.52,0.52,0.52,0.52,0.52,0.52,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,2.04,2.04,2.04,2.04,0.78,0.78,0.78`.
5. Check `opt_vat_rate`, `opt_sell_margin` and the battery values.
6. Leave `opt_dry_run` on.
7. Save and restart the add-on.

No HA URL or token is needed. The add-on uses the Supervisor token through `homeassistant_api`. `ha_url` and `ha_token` are hidden optional options; set them only to use a different Home Assistant instance.

### Docker and CLI

Without the Supervisor, sbam needs `ha_url` and a [long-lived access token](https://www.home-assistant.io/docs/authentication/#your-account-profile). Create it under **Profile → Security** in Home Assistant, preferably for a dedicated non-admin user. Pass the token as an environment variable so it stays out of `config.yaml` and the shell history.

```bash
export HA_URL=http://homeassistant.local:8123
export HA_TOKEN=<long-lived-token>
export SCHEDULER_MODE=optimize
export OPT_PRICE_ENTITY=sensor.nordpool_kwh_dk2_dkk_3_10_025
export OPT_CONSUMPTION_ENTITY=sensor.solarnet_power_load_consumed
export OPT_EV_ENTITY=sensor.tesla_wall_connector_total_power
export OPT_PURCHASE_SURCHARGE="0.52,0.52,0.52,0.52,0.52,0.52,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,2.04,2.04,2.04,2.04,0.78,0.78,0.78"
sbam schedule
```

In `config.yaml` the surcharges can also be a YAML list:

```yaml
scheduler_mode: optimize
ha_url: "http://homeassistant.local:8123"
opt_price_entity: sensor.nordpool_kwh_dk2_dkk_3_10_025
opt_consumption_entity: sensor.solarnet_power_load_consumed
opt_purchase_surcharge: [0.52, 0.52, 0.52, 0.52, 0.52, 0.52, 0.78, 0.78, 0.78, 0.78, 0.78, 0.78,
                         0.78, 0.78, 0.78, 0.78, 0.78, 2.04, 2.04, 2.04, 2.04, 0.78, 0.78, 0.78]
opt_dry_run: true
```

`pw_consumption` must be greater than 0 in optimize mode, because it is the static fallback for hours without history. See [Configuration → Cost Optimizer](configuration.md#cost-optimizer) for every key, its default and its valid range.

---

## Dry run first

### One-off plan with `sbam optimize`

`sbam optimize` computes the plan once, prints it and exits. It never writes to the inverter and never publishes to MQTT, whatever `opt_dry_run` says. `--dry-run=false` is rejected.

```bash
sbam optimize \
  -H 192.168.1.100 -k "$APIKEY" -u "$SOLCAST_URL" -c 11000 \
  --ha_url http://homeassistant.local:8123 \
  --opt_price_entity sensor.nordpool_kwh_dk2_dkk_3_10_025 \
  --opt_consumption_entity sensor.solarnet_power_load_consumed \
  --opt_purchase_surcharge "0.52,0.52,0.52,0.52,0.52,0.52,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,2.04,2.04,2.04,2.04,0.78,0.78,0.78"
# HA_TOKEN is read from the environment
```

Example output (rows shortened):

```
sbam optimize - DRY RUN (nothing written to the inverter)
Run at      2026-10-05 10:20 +02:00     Horizon  36 h (until 2026-10-06 21:59)
Battery     SoC 50.0 %  capacity 10 000 Wh  min SoC 7 %  usable 4 300 Wh
Model       efficiency 0.83  max charge 4 500 W  max discharge 4 500 W
Prices      sensor.nordpool_kwh_dk2_dkk_3_10_025 (DKK/kWh, incl. VAT)  VAT 0.25  sell margin 0.10
Consumption sensor.solarnet_power_load_consumed - EV sensor.tesla_wall_connector_total_power
            history 10 days; static fallback hours: none
PV          Solcast (forecast cache)

START             CONS Wh   PV Wh    BUY    SELL  ACTION         RATE W  SOC END %
Mon 10-05 10:00       333    1333   1.50    1.10  self_consume        0       58.1
Mon 10-05 11:00       500    2000   1.50    1.10  self_consume        0       70.7
...
Mon 10-05 16:00       500       0   1.50    1.10  hold                0      100.0
Mon 10-05 17:00       500       0   3.00    2.30  self_consume        0       94.9
Mon 10-05 18:00       500       0   3.00    2.30  discharge        4325       51.6
Mon 10-05 19:00       500       0   3.00    2.30  discharge        4464        7.0
...
Tue 10-06 03:00       500       0   0.80    0.54  charge           2241       25.6
Tue 10-06 04:00       500       0   0.80    0.54  charge           4482       62.8
...

Expected cost   -30.74 DKK  (import 17.45 - export 48.19 - terminal value 0.00)
Terminal value    0.00 DKK  (0 Wh left above min SoC x 0.83 x mean buy 1.63)
Decision        hour 0: self_consume 0 W - optimizer_self_consume_no_gain
Reasons         optimizer_self_consume_no_gain
```

The real output prints every hour. `--output json` prints the same object as the [`<prefix>/plan` topic](mqtt.md#optimizer-plan-topic). Exit codes: `0` plan computed, `1` configuration error or `--dry-run=false`, `2` optimizer fallback. See the [CLI reference](cli.md#optimize).

### Continuous dry run

Run `sbam schedule` (or the add-on) with `scheduler_mode: optimize` and `opt_dry_run: true`. Every hour:

- the log shows one `optimizer run` line with `dry_run=true`, the action, rate, reason, expected cost and horizon;
- from the second hour on, a `plan check` line compares the previous hour's planned SoC change with what the battery actually did (`planned_delta_wh`, `actual_delta_wh`, `deviation_wh`). The planned change runs from the plan's own start (`planned_soc_start_pct`), the actual change from the measured start (`actual_soc_start_pct`). They differ when the battery is below `opt_min_soc_pct` (the plan never starts below the minimum, while the inverter may discharge to its own lower floor) and by up to one planning step otherwise. If the action changed during the hour (EV guard, pause, `set_defaults`, `force_charge`, watchdog), `action_changes` lists each change with its time and cause, for example `["17:01 self_consume (ev_guard_released)"]`; a change identical to the one just before it (a button pressed several times in a row) is counted instead of listed again, with the time of the first press: `"05:37 charge (force_charge) x6"`. `action` is the action the hour started with, so the deviation then covers more than one action. `planned_pv_wh` and `planned_consumption_wh` are the hour's PV forecast and consumption profile the plan used: compare them with your PV and load sensors to see whether a deviation came from the forecast or the household. In dry run the inverter keeps running in self-consumption, so deviations in hours with a forced action are expected; in live mode they show how well the model (efficiency, rates, consumption, PV forecast) matches reality.
- a second Info line, `dry run: no Modbus registers written`, lists the registers a live run would write;
- with MQTT enabled, the [optimizer entities](mqtt.md#optimizer-entities-optimize-mode) update and **Optimizer Dry Run** is `on`.

The inverter keeps running on its own defaults. Compare the planned action with what happens in the house for a few days: do consumption, PV and prices look realistic? Is `static fallback hours` empty after the first 3 days?

---

## Before going live

The register mapping for `hold`, `standby` and `discharge`, and the inverter's revert timer, have not yet been verified on every Gen24+ firmware. Before you set `opt_dry_run: false`, check the following with someone watching the inverter (web UI or Fronius Solar.web) during the first live hours. Use the MQTT `force_charge`, `pause` and `set_defaults` commands to get out of any state that looks wrong.

| # | Check | How to observe it | Expected result |
|---|-------|-------------------|-----------------|
| V1 | `hold` | Wait for an hour planned as `hold` | The battery does not discharge. PV surplus still charges it. The battery is **not** charged from the grid. |
| V2 | `standby` | Wait for an hour planned as `standby` with PV surplus | The battery stays idle. PV surplus is exported, not curtailed. The next hour's action starts without delay. |
| V3 | `discharge` | Wait for an hour planned as `discharge` | The battery discharges at roughly the planned rate. Power beyond the house load is exported. Discharge stops at the inverter's own minimum SoC. |
| V4 | Minimum SoC | Inverter web UI → battery settings | The inverter's own minimum SoC is set, and `opt_min_soc_pct` is not below it. sbam keeps the Modbus reserve (`MinRsvPct`) at 0, so the inverter's setting is the hardware floor. |
| V5 | Rate limits | Inverter and battery data sheet; live log line `dispatch applied` (field `registers`) | The inverter's maximum battery charge power (`WChaMax`) matches your battery. `opt_max_charge_rate_w` and `opt_max_discharge_rate_w` are not above it. An implausible value leads to `optimizer_fallback_inverter_limits`. |
| V6 | Revert timer | During a `discharge` hour, stop sbam hard (for example `kill -9`, or pull the host's power) | Within about 65 minutes (3900 s) the inverter returns to normal self-consumption by itself. |
| V7 | Read-back | Live logs during the first forced action | No `optimizer_fallback_modbus_write_failed` caused by a read-back mismatch. A mismatch means the firmware reports different values than sbam wrote. |

!!! danger "If V6 fails"
    If the inverter does not revert by itself, a forced `discharge` or `standby` stays active when sbam is killed, crashes or loses power, until sbam starts again and writes defaults. Graceful stops, pause, fallbacks and the watchdog still restore defaults, but nothing covers a hard crash. Report the result in an issue, and consider staying in dry run.

To go live, set `opt_dry_run: false` and restart. The first run writes immediately. The log now shows `dry_run=false` and **Optimizer Dry Run** turns `off`.

---

## Fail-safes

All fail-safes write the inverter **defaults** (`self_consume`), with the storage-control mode reset first. In dry run the optimizer never forces anything, so the fail-safes stay silent. The one exception: if you sent a `force_charge` command, `pause` and shutdown restore the defaults even in dry run.

| Trigger | What sbam does |
|---------|----------------|
| Every forced action (`charge`, `hold`, `standby`, `discharge`) | Also writes a revert timeout of 3900 s. The hourly run refreshes it. If sbam stops re-asserting, the inverter reverts within about 65 minutes (V6). |
| Shutdown (SIGTERM / SIGINT, add-on stop) | Writes defaults within 5 s if the last applied action was not `self_consume`. |
| Startup validation error in live mode | Attempts defaults (5 s deadline) before exiting with code 1, so a broken config edit does not leave a forced state behind. |
| `pause` command | Writes defaults first, then pauses. See [MQTT commands](#mqtt-commands-in-optimize-mode). |
| Missing or implausible input, invalid plan, solver error | Writes defaults, publishes an `optimizer_fallback_*` reason and retries at the next full hour. |
| Modbus write or read-back failure | One defaults attempt, Error log, message on `<prefix>/error`, retry at the next full hour. |
| SoC watchdog | While `discharge` is applied, SoC is checked every 15 minutes. At or below `opt_min_soc_pct` + 1 %, or when SoC cannot be read, defaults are written with reason `optimizer_watchdog_min_soc`. The watchdog never starts a forced action. |

On fallback, **Optimizer Action** shows `self_consume`, **Last Decision** shows `fallback_defaults`, and all **Plan +NNh** sensors become `unavailable`.

### Who can steer the battery

In optimize mode the battery follows whatever the Home Assistant price and consumption sensors say. Anyone who can change those sensor states — any Home Assistant user, an automation, or a holder of a long-lived token (for example via `POST /api/states/...`) — can make sbam charge from the grid at an expensive hour or discharge and export at a cheap one. sbam rejects implausible, stale and inconsistent prices and falls back to the inverter defaults, but a believable fake value is still acted on. The damage is limited to roughly one or two battery cycles a day times the price spread, plus wear.

The same applies to anyone who can publish to sbam's MQTT command topics. Treat write access to these sensors and topics like access to the inverter: limit who has Home Assistant admin rights and long-lived tokens, and protect the broker with authentication and ACLs (see [MQTT commands](#mqtt-commands-in-optimize-mode)).

---

## MQTT commands in optimize mode

Commands use the same topics and payloads as in the other modes (see [MQTT Guide](mqtt.md#control-buttons)), but some behave differently:

| Command | Behaviour in optimize mode |
|---------|----------------------------|
| `trigger_now` | Runs the optimizer immediately. Limited to one accepted run per 60 s; extra commands get `accepted: false, error: "rate limited"`. |
| `pause` | Writes defaults **first** (in dry run only after a `force_charge` command), then pauses. While paused, hourly runs still compute and publish the plan with reason `optimizer_paused` but write nothing. If the defaults write fails, the pause is still set and the ack carries the error. |
| `resume` | Clears the pause and runs the optimizer immediately. Pause expiry does the same. |
| `set_defaults` | Always accepted, even while paused. Writes defaults and ends a manual override. The next hourly run applies the plan again. |
| `force_charge` | Rejected while paused. Otherwise it charges as in the other modes and also writes a revert timeout of `duration_s` + 300 s (end of the current hour + 300 s without `duration_s`). It starts a **manual override** until `duration_s` has passed, or until the end of the current hour. During the override, runs publish the plan with reason `optimizer_manual_override` and write nothing. `target_pct: 0` restores defaults and ends the override. |
| `set_reserve` | Rejected with `accepted: false, error: "not supported in scheduler_mode=optimize"`. The optimizer uses `opt_min_soc_pct`. |

!!! warning "Protect your broker"
    In optimize mode, anyone who can publish to `<prefix>/cmd/#` can make the battery charge or stop it. Enable broker authentication and restrict `<prefix>/cmd/#` with an ACL to sbam's Home Assistant user. See [MQTT Guide → Broker security](mqtt.md#broker-security-in-optimize-mode).

---

## Reason codes

`last_decision_reason` carries the single most important code. `opt_reasons` (state) and `reasons` (plan) carry all applicable codes. Precedence: **fallback > override > degraded input > action**. Dry run is not a reason code; it is shown by `opt_dry_run`.

### Fallback (defaults restored, retried next hour)

| Code | Cause | What to do |
|------|-------|-----------|
| `optimizer_fallback_ha_unreachable` | HA API connection error, timeout or 5xx. Common while HA restarts. | Nothing if it clears next hour. Otherwise check `ha_url` and the network. |
| `optimizer_fallback_ha_auth_failed` | HA returned 401/403. | Docker/CLI: create a new token and update `HA_TOKEN`. Add-on: check that `homeassistant_api` is granted. |
| `optimizer_fallback_price_entity_not_found` | `opt_price_entity` does not exist, or its state is `unknown`/`unavailable`. | Check the entity ID in **Developer Tools → States**. |
| `optimizer_fallback_price_format` | The Nordpool attributes cannot be parsed, or the unit is not per kWh/MWh. | Check that the sensor has `raw_today` or `today` attributes. |
| `optimizer_fallback_missing_prices` | No price covers the current hour, or the price sensor has not been updated for 26 h. | Check the Nordpool integration. |
| `optimizer_fallback_price_inconsistent` | The sensor state does not match the price entry for the current hour, or the price entries are inconsistent. | Usually transient; reload the Nordpool integration if it persists. |
| `optimizer_fallback_implausible_input` | An input is not a finite number or is outside its plausibility bounds (for example a price outside −5..50 per kWh). | Check the sensors named in the log line. |
| `optimizer_fallback_soc_unavailable` | SoC or capacity could not be read from the Fronius Solar API. | Check `fronius_ip` and that the Solar API is enabled. |
| `optimizer_fallback_solver_error` | The optimizer failed or exceeded its time budget. | Report an issue with the log. |
| `optimizer_fallback_invalid_plan` | The finished plan failed its safety check, or no slot covers the current time. | Report an issue with the log. |
| `optimizer_fallback_inverter_limits` | `WChaMax` or its scale factor is outside the expected range, so rates cannot be converted safely. | See V5. |
| `optimizer_fallback_modbus_write_failed` | Writing the action failed or the read-back did not match. Defaults were attempted. | Check inverter connectivity and Modbus settings; see V7. |
| `optimizer_watchdog_min_soc` | The discharge watchdog found SoC at or near `opt_min_soc_pct`, or could not read SoC. | None; the next hourly run plans again. |

### Override (plan published, nothing written)

| Code | Cause |
|------|-------|
| `optimizer_paused` | sbam is paused. |
| `optimizer_manual_override` | A `force_charge` override is active. |

### Guard (applied action differs from the plan)

| Code | Cause |
|------|-------|
| `optimizer_ev_guard_hold` | The EV draws more than `opt_ev_guard_w` in a `self_consume` hour, so `hold` is applied ([EV guard](#ev-guard-opt_ev_guard_w)). |

### Degraded input (plan still applied)

| Code | Cause | What to do |
|------|-------|-----------|
| `optimizer_consumption_static_fallback` | At least one hour of day has fewer than 3 days of usable history; `pw_consumption / 24` is used for it. | Normal for the first days. Check that `pw_consumption` is realistic. |
| `optimizer_consumption_entity_not_found` | The consumption entity does not exist or has no history; the whole profile is static. | Check `opt_consumption_entity` and the recorder. |
| `optimizer_ev_entity_not_found` | `opt_ev_entity` is set but not found; EV subtraction is skipped. | Check `opt_ev_entity`. |
| `optimizer_pv_forecast_missing` | Solcast has no data for some horizon hours; they count as 0 Wh. | Check the Solcast quota and `url`/`apikey`. On the free plan (10 requests per site per day), `cache_time` of about 3 h (`10800`) spreads the requests over the daylight hours; once the limit is reached sbam waits for the 00:00 UTC reset. |
| `optimizer_pv_forecast_stale` | The cached forecast is older than `max(cache_time, 24 h)` and is treated as missing. | Check that Solcast requests succeed. |

### Action (one per run, for the current hour)

| Action | Code | Meaning |
|--------|------|---------|
| `charge` | `optimizer_charge_ahead_of_peak` | Grid-charging now because a later price is higher than now ÷ η. |
| `charge` | `optimizer_charge_negative_price` | The buy price is 0 or negative this hour. |
| `hold` | `optimizer_hold_for_later_peak` | The battery's energy is worth more later than now. |
| `standby` | `optimizer_standby_export_pv` | Export PV now at a good sell price; later PV fills the battery. |
| `discharge` | `optimizer_export_high_price` | Selling now beats keeping the energy. |
| `discharge` | `optimizer_discharge_before_cheap_charge` | Discharge now because the battery can be refilled more cheaply before the next peak. |
| `standby` | `optimizer_discharge_clamped_min_soc` | The plan wanted to discharge, but the battery is within 1 % of `opt_min_soc_pct`, so standby is applied instead. |
| `self_consume` | `optimizer_self_consume_optimal` | Default inverter behaviour is the cheapest. |
| `self_consume` | `optimizer_self_consume_no_gain` | No forced action saves at least its threshold this hour ([minimum gain and wear cost](#minimum-gain-and-wear-cost-opt_min_gain-opt_wear_cost); with both `0`: none is cheaper than the default). |

---

## Troubleshooting

| Symptom | Cause and fix |
|---------|---------------|
| Startup stops with `invalid configuration for scheduler_mode=optimize (N problems)` and exit code 1 | All problems are listed in one message. Fix them and restart. |
| `home assistant access: no Supervisor token found and ha_url/ha_token are not set` | Docker/CLI: set `HA_URL` and `HA_TOKEN`. Add-on: make sure the add-on version grants `homeassistant_api`. |
| `opt_purchase_surcharge: expected exactly 24 comma-separated numbers ... got 48` | A decimal comma (`0,52`) splits into two values. Use `.` as the decimal separator. |
| `opt_vat_rate: must be a fraction between 0 and 1, got 25; did you mean 0.25?` | Enter VAT as a fraction. |
| Warning `ha_url uses plain http to a non-private host` | The token travels unencrypted. Use `https`. |
| Plan sensors beyond a certain hour are `unavailable` | Normal: the horizon ends at the last published price. |
| All plan sensors `unavailable`, Optimizer Action `self_consume` | A fallback. Read **Decision Reason** and use the [reason codes](#reason-codes). |
| Expected Cost has no unit | The currency could not be read from the price sensor yet. It is filled after the first successful price read. |
| The plan grid-charges before a sunny day | PV is missing (`optimizer_pv_forecast_missing` / `_stale`) and counted as 0 Wh. Check Solcast. |
| `trigger_now` is rejected with `rate limited` | Wait 60 s between triggers. |
| `set_reserve` is rejected | Expected in optimize mode; use `opt_min_soc_pct`. |
| Leaving optimize mode | Set `scheduler_mode` back to `windows` or `crontab`. The optimizer entities are removed from Home Assistant and `<prefix>/plan` is cleared. |

---

## Privacy

The plan, the consumption profile and the 36 per-hour sensors reveal when the household is home and active. sbam publishes hourly aggregates only, never raw history samples, and only to your own MQTT broker. The `<prefix>/plan` message is **retained** on the broker, and Home Assistant's recorder stores the sensor history.

- Restrict who can subscribe to `<prefix>/#` on the broker.
- Exclude the per-hour plan sensors from the recorder; they change every hour and the full plan is already in the Optimizer Action attributes:

```yaml
recorder:
  exclude:
    entity_globs:
      - sensor.sbam_opt_plan_*
```

- Remove plan data from logs and payloads before posting them in a bug report.
