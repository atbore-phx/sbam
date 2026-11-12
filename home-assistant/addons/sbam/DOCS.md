# sbam — Smart Battery Advanced Manager

Charge your Fronius Gen24+ battery intelligently using Solcast weather forecasts and MQTT.

## Documentation

Full documentation, prerequisites, installation screenshots, configuration references, MQTT guide, CLI reference, and changelog are available at this link: **[Documentation](https://atbore-phx.github.io/sbam/)**

## Cost-optimized dispatch (optimize mode)

With `scheduler_mode: optimize`, sbam plans every hour whether the battery should charge, hold, stand by or discharge, and at what rate, so that the expected electricity cost over the known price horizon is as low as possible. It combines the Solcast forecast, your consumption history and the hourly prices. The `crontab` and `windows` settings are ignored in this mode.

- **No token needed.** The add-on reads Home Assistant through the Supervisor (`homeassistant_api`); leave `optimizer.ha_url` and `optimizer.ha_token` empty.
- **Required entities.** `optimizer.price_entity`: the Nordpool price sensor (prices incl. VAT); not needed with a fixed tariff. `optimizer.consumption_entity`: a household load power sensor (W or kW). Optional: `optimizer.ev_entity`, an EV charger power sensor whose load is excluded from the consumption profile. With it set, the EV guard (`optimizer.ev_guard_w`, default 1000 W) keeps the battery from discharging into the car in self-consumption hours. The recorder must keep at least `optimizer.history_days` (default 10) days of history. `pw_consumption` must be greater than 0; it is the fallback for hours without enough history.
- **Surcharge format.** `optimizer.purchase_surcharge` takes exactly 24 comma-separated values in currency/kWh incl. VAT, one per hour starting at 00:00. Use `.` as the decimal separator; each value must be between -5 and 10. Example: `0.52,0.52,0.52,0.52,0.52,0.52,0.78,0.78,...` (24 values in total).
- **Fixed tariff.** Without spot prices, set `optimizer.price_source` to `static` and enter your buy and feed-in price under `optimizer.static`: `buy_price` and `sell_price` each take exactly 24 comma-separated values in currency/kWh, one per hour starting at 00:00 in the Home Assistant time zone, each between -5 and 50. `currency` is optional. The price sensor, VAT rate, sell margin and surcharge are then not used.
- **Start in dry run.** `optimizer.dry_run` is on by default: the plan is computed and published over MQTT, but nothing is written to the inverter. Watch the plan and the decision reasons in Home Assistant for a few days first.

> **Warning:** Only turn `optimizer.dry_run` off after you have verified how your inverter behaves when sbam forces charging and discharging. Live mode writes charge and discharge commands to the inverter every hour.

Full reference: **[Cost optimizer documentation](https://atbore-phx.github.io/sbam/optimizer/)**
