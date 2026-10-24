# sbam — Smart Battery Advanced Manager

Charge your Fronius Gen24+ battery intelligently using Solcast weather forecasts and MQTT.

## Documentation

Full documentation, prerequisites, installation screenshots, configuration references, MQTT guide, CLI reference, and changelog are available at this link: **[Documentation](https://atbore-phx.github.io/sbam/)**

## Cost-optimized dispatch (optimize mode)

With `scheduler_mode: optimize`, sbam plans every hour whether the battery should charge, hold, stand by or discharge, and at what rate, so that the expected electricity cost over the known Nordpool price horizon is as low as possible. It combines the Solcast forecast, your consumption history and the hourly prices. The `crontab` and `windows` settings are ignored in this mode.

- **No token needed.** The add-on reads Home Assistant through the Supervisor (`homeassistant_api`); leave `ha_url` and `ha_token` empty.
- **Required entities.** `opt_price_entity`: the Nordpool price sensor (prices incl. VAT). `opt_consumption_entity`: a household load power sensor (W or kW). Optional: `opt_ev_entity`, an EV charger power sensor whose load is excluded from the consumption profile. With it set, the EV guard (`opt_ev_guard_w`, default 1000 W) keeps the battery from discharging into the car in self-consumption hours. The recorder must keep at least `opt_history_days` (default 10) days of history. `pw_consumption` must be greater than 0; it is the fallback for hours without enough history.
- **Surcharge format.** `opt_purchase_surcharge` takes exactly 24 comma-separated values in currency/kWh incl. VAT, one per hour starting at 00:00. Use `.` as the decimal separator; each value must be between -5 and 10. Example: `0.52,0.52,0.52,0.52,0.52,0.52,0.78,0.78,...` (24 values in total).
- **Start in dry run.** `opt_dry_run` is on by default: the plan is computed and published over MQTT, but nothing is written to the inverter. Watch the plan and the decision reasons in Home Assistant for a few days first.

> **Warning:** Only turn `opt_dry_run` off after you have verified how your inverter behaves when sbam forces charging and discharging. Live mode writes charge and discharge commands to the inverter every hour.

Full reference: **[Cost optimizer documentation](https://atbore-phx.github.io/sbam/optimizer/)**
