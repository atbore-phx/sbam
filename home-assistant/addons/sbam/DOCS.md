## Home Assistant App Installation and How to Use

This document provides instructions for installing and configuring the SBAM app (add-on) for Home Assistant OS (HAOS). It covers prerequisites, installation steps and configuration options.

### Prerequisites
----
SBAM requires the following prerequisites to function correctly:
[SBAM prerequisites](https://github.com/atbore-phx/sbam/blob/main/docs/prereq.md)

For MQTT features in Home Assistant, enable MQTT support first:
[Home Assistant MQTT integration](https://www.home-assistant.io/integrations/mqtt/)

The recommended broker for Home Assistant users is the Mosquitto add-on.

### Home Assistant Add-on Installation
----
SBAM is available as an App (formerly known as add-ons) for HAOS (Home Assistant OS).

**HAOS must be able to reach the Fronius inverter on its LAN IP.**


Official guide:
[Install a third-party Home Assistant app repository](https://www.home-assistant.io/common-tasks/os#installing-a-third-party-app-repository)

1. Settings
2. Apps

<img width="430" height="332" alt="chrome_ChiVwNM3hN" src="https://github.com/user-attachments/assets/c83e60ba-fbd1-4138-a161-01353ca55eaa" />


3. Install app

<img width="175" height="76" alt="image" src="https://github.com/user-attachments/assets/f9a9a799-465d-45bf-9241-0ce35587fb4f" />


4. Repositories

<img width="489" height="82" alt="image" src="https://github.com/user-attachments/assets/88aace46-bb21-4669-91e9-6a33488519c6" />


5. Add
6. Add repository URL:
https://github.com/atbore-phx/sbam

<img width="449" height="220" alt="image" src="https://github.com/user-attachments/assets/ed59cc24-dd50-4e0d-a84d-708643c0994c" />


Once added:

1. If the add-on is not visible, refresh the page
2. Click the SBAM add-on

![image](https://github.com/user-attachments/assets/ec81f283-fc97-4328-8e1e-ffbd3c4d2e29)

3. Click **Install**

![chrome_NT8Mrf6ls1](https://github.com/atbore-phx/sbam/assets/11421185/cb9eafe3-a274-4164-a789-1c31a87308e1)

4. Enable Start on boot and Watchdog

![chrome_JsiS3CyShs](https://github.com/atbore-phx/sbam/assets/11421185/413e2d3d-638b-417c-b906-34d46aee62c0)

### Configuration

Before starting, open the Configuration tab and set options as needed.

- **url**: Solcast forecast site address (replace <YOUR-SITE> with your identifier). Multiple addresses are supported (max 2), separated by comma.
- **apikey**: Solcast API key.
- **fronius_ip**: Fronius inverter LAN IP.
- **start_hr**: Start time of the advantageous network operator rate (default 00:00). Cross-midnight ranges are supported.
- **end_hr**: End time of the advantageous network operator rate (default 06:00). Cross-midnight ranges are supported.
- **crontab**: Schedule to run SBAM (default 00 00-05 * * *). To disable scheduled execution, set to `0 0 0 0 0` (runs once at startup waiting mqtt commands if mqtt is enabled).
- **pw_consumption**: Daily electrical consumption in Wh (default 11000).
- **max_charge**: Maximum grid charging power in W (default 3500).
- **pw_lwt**: Hysteresis lower threshold offset in Wh to stop charging (default 0).
- **pw_upt**: Hysteresis upper threshold offset in Wh to start charging (default 0).
- **pw_batt_reserve**: Minimum battery capacity to maintain in Wh (default 4000).
- **batt_reserve_start_hr**: Start time for reserve window (empty uses start_hr). Uses the same cross-midnight behavior as the main window.
- **batt_reserve_end_hr**: End time for reserve window (empty uses end_hr). Uses the same cross-midnight behavior as the main window.
- **defaults**: At end of crontab cycle, reconfigure Fronius inverter to defaults.
- **reset**: At add-on startup, reconfigure Fronius inverter to defaults.
- **debug**: Increase log level.
- **log_type**: Logger output mode.
- **cache_forecast**: Enable forecast caching (default false).
- **cache_file_prefix**: Forecast cache file prefix (default cached_forecast).
- **cache_time**: Forecast cache TTL in seconds (default 7200).
- **forecast_horizon**: Forecast window selection mode (default `default`).
  - `default` — current behavior: forecast for today before 12:00, for tomorrow after 12:00.
  - `next_solar_day` — same as `default`; targets the upcoming useful solar day, appropriate for night charging.
  - `remaining_today` — forecast only the intervals from now through end of the local day.
  - `today` — forecast the full local calendar day.
  - `tomorrow` — forecast the full next local calendar day.
  - `off` — skip forecast retrieval entirely; only reserve charging is active (no Solcast API calls).
- **consumption_horizon**: Daily consumption model (default `full_day`). Options: `full_day` (use `pw_consumption` as-is), `remaining_today` (scale proportionally to time remaining in the local day).
- **windows**: Optional ordered list of charge windows replacing the single `start_hr`/`end_hr`/`max_charge` triple. When configured, legacy `start_hr`/`end_hr` keys must not be specified. Each window accepts:
  - `name` (string, optional) — human-readable label shown in MQTT/logs; auto-generated if empty.
  - `start` (string, required) — window start time in `HH:MM` format.
  - `end` (string, required) — window end time in `HH:MM` format. Cross-midnight ranges are supported.
  - `max_charge` (float, required) — maximum grid charging power in W for this window.
  - `forecast_horizon` (string, optional) — per-window forecast horizon override; defaults to top-level `forecast_horizon`.
  - `consumption_horizon` (string, optional) — per-window consumption horizon override; defaults to top-level `consumption_horizon`.
  Example:
  ```yaml
  windows:
    - name: "night"
      start: "02:00"
      end: "06:00"
      max_charge: 3500
      forecast_horizon: "tomorrow"
    - name: "midday"
      start: "12:00"
      end: "15:00"
      max_charge: 2000
  ```
  This option is also available via CLI `--windows` (repeatable CSV) and `--windows-json` (JSON).
- **mqtt_enabled**: Enable MQTT publishing and command/discovery integration (default false).
- **mqtt_broker**: Optional MQTT broker URL, for example tcp://broker:1883. if empty and MQTT is enabled, SBAM tries to auto-fill from Home Assistant service data.
- **mqtt_client_id**: Optional MQTT client identifier. if empty and MQTT is enabled, SBAM generates a random client ID at runtime.
- **mqtt_username**: Optional MQTT username. if empty and MQTT is enabled, SBAM tries to auto-fill from Home Assistant service data.
- **mqtt_password**: Optional MQTT password. if empty and MQTT is enabled, SBAM tries to auto-fill from Home Assistant service data.
- **mqtt_topic_prefix**: Prefix for SBAM state, availability, and command topics (default SBAM).
- **mqtt_ha_discovery**: Enable retained Home Assistant discovery config publishing (default true when MQTT is enabled).
- **mqtt_ha_discovery_prefix**: Home Assistant discovery root topic prefix (default homeassistant).

Save the configuration after editing options.

![SBAM-conf](https://github.com/user-attachments/assets/d0eab452-7b77-4d2c-9b24-7ac44fd50b7a)

### Start SBAM

Start SBAM after configuration is complete.

![chrome_5OngSH5IRc](https://github.com/atbore-phx/sbam/assets/11421185/9575b453-5132-4a24-9166-bc6d385690f1)

Check add-on logs for startup and runtime details.

### MQTT Integration

When MQTT is enabled, SBAM publishes state and availability topics and subscribes to command topics for Home Assistant integration.\
If MQTT broker details are not provided in the configuration, SBAM attempts to auto-fill them from Home Assistant service data.
For full setup, topic mapping, payload schemas, command examples, and migration
details, see [MQTT documentation](https://github.com/atbore-phx/sbam/blob/main/docs/mqtt.md).

