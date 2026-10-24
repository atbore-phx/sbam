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
6. Model Type: int +SF

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

After adding your installation, you will obtain a forecast link:

```
https://api.solcast.com.au/rooftop_sites/your-site/forecasts?format=json
```

Copy it — you will need it for the `url` configuration option.

### API Key

1. Click on your name at the top right on the Solcast site
2. Select **Your API Key**
3. Generate the API key and copy it — you will need it for the `apikey` configuration option

## Cost Optimizer Prerequisites

These are needed only for `scheduler_mode: optimize` ([Cost-optimized Dispatch](optimizer.md)).

### Home Assistant entities

| Entity | Example | Requirements |
|--------|---------|--------------|
| Spot price sensor (`opt_price_entity`) | `sensor.nordpool_kwh_dk2_dkk_3_10_025` | From the [Nordpool integration](https://github.com/custom-components/nordpool), configured **with VAT** in your currency. Unit per kWh (or per MWh). Needs the `raw_today`/`raw_tomorrow` or `today`/`tomorrow` attributes; hourly and 15-minute prices both work. |
| Household load power sensor (`opt_consumption_entity`) | `sensor.solarnet_power_load_consumed` | Instantaneous power of the whole house in W or kW, recorded by the HA recorder. |
| EV charger power sensor (`opt_ev_entity`, optional) | `sensor.tesla_wall_connector_total_power` | Power in W or kW. Its energy is subtracted from the household load, so EV charging does not distort the profile. |

The recorder must keep at least `opt_history_days` days of history (default 10). Home Assistant's default `purge_keep_days` is 10, which matches. Sensors must be lower-case `sensor.<object_id>` entity IDs.

### Home Assistant API access

sbam reads states and history from the Home Assistant REST API (GET only).

- **HA add-on:** nothing to configure. The add-on requests `homeassistant_api` access and uses the Supervisor token automatically.
- **Docker / standalone binary:** create a long-lived access token in Home Assistant under **Profile → Security → Long-lived access tokens**, ideally for a dedicated non-admin user. Set `ha_url` (e.g. `http://homeassistant.local:8123`) and pass the token as the `HA_TOKEN` environment variable. Use `https` when Home Assistant is not on your local network.

### Solcast

The optimizer always reads the PV forecast through the forecast cache. Make sure Solcast requests succeed; when the forecast is missing or older than `max(cache_time, 24 h)`, PV is counted as 0 Wh and the plan becomes pessimistic.
