# Prerequisites

sbam requires these prerequisites to function correctly.

## Allow Battery Charging from the Public Grid

Check if your inverter permits battery charging from the public grid:

1. Open the web interface of the inverter
2. Select **Device Configuration → Components**
3. Expand the battery section
4. Enable **"Allow Battery Charging from the Public Grid"**

![Allow charge setting](https://github.com/user-attachments/assets/3b366999-cf9c-4003-93d4-654d137ba001)

## Enable Modbus and Solar API

### Modbus

Remote control of the Fronius inverter's charge requires enabling the **"Slave as Modbus TCP"** function.

1. Open the web interface of the inverter
2. Select the **Communication** section
3. Open the **Modbus** menu
4. Activate **Slave (Secondary inverter) Modbus TCP**
5. TCP port: 502
6. Model Type: `int + SF` (default) or `float`. With `float`, set [`sunspec_type`](configuration.md#inverter-connection) to `float` or `auto`.

![Modbus settings](https://github.com/user-attachments/assets/afadb0b5-1edb-461c-919a-9fd249029f94)

### Solar API

sbam uses the local Fronius API to retrieve battery data:

1. Open the web interface of the inverter
2. Select the **Communication** section
3. Open the **Solar API** menu
4. Enable **Solar API Communication**

![Solar API setting](https://github.com/atbore-phx/sbam/assets/11421185/818eddd1-678f-45ba-8081-9958882786cf)

## Solcast Forecasting Service

sbam uses [Solcast](https://solcast.com/free-rooftop-solar-forecasting) for weather forecasts and solar production estimates. The free tier allows up to 10 API calls per day.

sbam can get the forecast in two ways, chosen with [`pv_source`](configuration.md#pv-forecast):

- **Option A, `pv_source: solcast_api` (default):** sbam downloads the forecast from Solcast itself with your forecast URL and API key.
- **Option B, `pv_source: ha_solcast`:** sbam reads the forecast from the *Solcast PV Forecast* integration in Home Assistant, including the dampening and hard limit you set up there. sbam then makes no Solcast requests.

!!! note "One Solcast quota"
    If the Home Assistant integration and sbam both use the same free-plan API key, they share its 10 calls per day. With option B only the integration calls Solcast, so it gets the whole quota.

### Option A: Solcast API

After adding your installation, you will obtain a forecast link:

```
https://api.solcast.com.au/rooftop_sites/your-site/forecasts?format=json
```

Copy it — you will need it for the `url` configuration option.

#### API Key

1. Click on your name at the top right on the Solcast site
2. Select **Your API Key**
3. Generate the API key and copy it — you will need it for the `apikey` configuration option

### Option B: Home Assistant Solcast integration

1. **Install the integration.** Install *Solcast PV Forecast* ([`BJReplay/ha-solcast-solar`](https://github.com/BJReplay/ha-solcast-solar)) from HACS, restart Home Assistant and add the integration under **Settings → Devices & services**. Enter your Solcast API key there, not in sbam. Wait until it has fetched a forecast at least once.
2. **Keep the half-hourly breakdown on.** In **Settings → Devices & services → Solcast PV Forecast → Configure**, **Enable half-hourly breakdown attribute** (`attr_brk_halfhourly`) must be on. It is on by default. Without it the forecast sensors have no `detailedForecast` attribute, and sbam logs a Warn that says so.
3. **Optional: dampening and hard limit.** Set them up in the integration. sbam uses the forecast as the integration delivers it (`pv_estimate`, the 50 % estimate).
4. **Find the entity IDs.** Open **Settings → Devices & services → Solcast PV Forecast → Entities** and note the IDs of **Forecast today**, **Forecast tomorrow** and **API last polled**. On an English Home Assistant they are the sbam defaults:

    | sbam key | Default entity ID |
    |----------|-------------------|
    | `pv_entity_today` | `sensor.solcast_pv_forecast_forecast_today` |
    | `pv_entity_tomorrow` | `sensor.solcast_pv_forecast_forecast_tomorrow` |
    | `pv_entity_last_polled` | `sensor.solcast_pv_forecast_api_last_polled` |

    If Home Assistant runs in another language, or you renamed the entities, the IDs differ (for example `sensor.solcast_pv_forecast_prognose_heute` on a German install). Copy them into the three keys. They must be lower-case `sensor.<object_id>` IDs.
5. **Home Assistant API access.** In the add-on there is nothing to set. Docker and standalone need `ha_url` and a token in `HA_TOKEN`; see [Home Assistant API access](#home-assistant-api-access).
6. **Check it.** Run `sbam estimate` with the same configuration. It prints the day's total from the integration, or names the entity that failed and exits with code 1.

!!! warning "Write access to the PV entities"
    With `pv_source: ha_solcast`, write access to the configured PV entities = influence over grid charging. Anything that can change their state or attributes in Home Assistant can steer when sbam charges from the grid. See [Configuration → PV Forecast](configuration.md#pv-forecast).

## Cost Optimizer Prerequisites

These are needed only for `scheduler_mode: optimize` ([Cost-optimized Dispatch](optimizer.md)).

### Home Assistant entities

| Entity | Example | Requirements |
|--------|---------|--------------|
| Spot price sensor (`optimizer.price_entity`) | `sensor.nordpool_kwh_dk2_dkk_3_10_025` | From the [Nordpool integration](https://github.com/custom-components/nordpool), configured **with VAT** in your currency. Unit per kWh (or per MWh). Needs the `raw_today`/`raw_tomorrow` or `today`/`tomorrow` attributes; hourly and 15-minute prices both work. |
| Household load power sensor (`optimizer.consumption_entity`) | `sensor.solarnet_power_load_consumed` | Instantaneous power of the whole house in W or kW, recorded by the HA recorder. |
| EV charger power sensor (`optimizer.ev_entity`, optional) | `sensor.tesla_wall_connector_total_power` | Power in W or kW. Its energy is subtracted from the household load, so EV charging does not distort the profile. |

The recorder must keep at least `optimizer.history_days` days of history (default 10). Home Assistant's default `purge_keep_days` is 10, which matches. Sensors must be lower-case `sensor.<object_id>` entity IDs.

### Home Assistant API access

sbam reads states and history from the Home Assistant REST API (GET only). The same access is used with `pv_source: ha_solcast` in every scheduler mode and by `sbam estimate`; see [Configuration → Home Assistant API](configuration.md#home-assistant-api).

- **HA add-on:** nothing to configure. The add-on requests `homeassistant_api` access and uses the Supervisor token automatically.
- **Docker / standalone binary:** create a long-lived access token in Home Assistant under **Profile → Security → Long-lived access tokens**, ideally for a dedicated non-admin user. Set `optimizer.ha_url` (e.g. `http://homeassistant.local:8123`) and pass the token as the `HA_TOKEN` environment variable. Use `https` when Home Assistant is not on your local network.

### Solcast

With `pv_source: ha_solcast`, PV comes from the Home Assistant Solcast integration; see [Option B](#option-b-home-assistant-solcast-integration). A forecast whose last poll is older than 24 h is treated as stale.

With `pv_source: solcast_api`, the optimizer always reads the PV forecast through the forecast cache. Make sure Solcast requests succeed; when the forecast is missing or older than `max(cache_time, 24 h)`, PV is counted as 0 Wh and the plan becomes pessimistic.
