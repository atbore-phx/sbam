## Installation and How to Use

### Prerequisites

sbam requires the following prerequisites to function correctly:
https://github.com/atbore-phx/sbam/blob/main/docs/prereq.md

For MQTT features in Home Assistant, enable MQTT support first:
https://www.home-assistant.io/integrations/mqtt/

The recommended broker for Home Assistant users is the Mosquitto add-on.

### Home Assistant Add-on Installation

Sbam is available as an App (formerly known as add-ons) for HAOS (Home Assistant OS).

**HAOS must be able to reach the Fronius inverter on its LAN IP.**


Official guide:
https://www.home-assistant.io/common-tasks/os#installing-a-third-party-app-repository

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
2. Click the sbam add-on

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
- **crontab**: Schedule to run sbam (default 00 00-05 * * *).
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
- **mqtt_enabled**: Enable MQTT publishing and command/discovery integration (default false).
- **mqtt_broker**: MQTT broker URL, for example tcp://broker:1883.
- **mqtt_client_id**: Optional MQTT client identifier.
- **mqtt_username**: Optional MQTT username.
- **mqtt_password**: Optional MQTT password.
- **mqtt_topic_prefix**: Prefix for sbam state, availability, and command topics (default sbam).
- **mqtt_ha_discovery**: Enable retained Home Assistant discovery config publishing (default true when MQTT is enabled).
- **mqtt_ha_discovery_prefix**: Home Assistant discovery root topic prefix (default homeassistant).

Save the configuration after editing options.

Example overnight configuration: `start_hr: 22:00` and `end_hr: 06:00`.
Reserve windows can also cross midnight, for example `batt_reserve_start_hr: 23:00`
and `batt_reserve_end_hr: 05:00`. Equal start and end values are invalid.

![sbam-conf](https://github.com/user-attachments/assets/d0eab452-7b77-4d2c-9b24-7ac44fd50b7a)

### MQTT Behavior in the Add-on

When MQTT is enabled:

- If mqtt_broker is already set, sbam keeps user-provided broker and credentials.
- If mqtt_broker is empty, sbam tries to auto-fill broker and credentials from Home Assistant service data via bashio::services mqtt.
- Auto-fill only applies to empty values and does not overwrite manual broker, username, or password.

TLS add-on options are intentionally not exposed in the Home Assistant add-on UI for v2.0.0.
Standalone sbam still supports TLS options via CLI, env vars, and config.yaml.

### MQTT Topic Map

Assuming default mqtt_topic_prefix=sbam:

- State: sbam/state
- Errors: sbam/error
- Availability: sbam/availability (retained online/offline)
- Commands:
  - sbam/cmd/trigger_now
  - sbam/cmd/force_charge
  - sbam/cmd/set_defaults
  - sbam/cmd/pause
  - sbam/cmd/resume
- Command acknowledgements: sbam/cmd/<command>/ack

Discovery entities are published under mqtt_ha_discovery_prefix, for example:
homeassistant/<component>/sbam/<object_id>/config

### MQTT Examples

Read state updates:

```bash
mosquitto_sub -h <broker-host> -t 'sbam/state' -v
```

Send force charge command:

```bash
mosquitto_pub -h <broker-host> -t 'sbam/cmd/force_charge' -m '{"target_pct":100,"duration_s":3600}'
```

### About Show in Sidebar and Auto Update

- Show in sidebar: sbam currently has no ingress web UI. There is no sidebar panel to expose for this add-on.
- Auto update: this toggle is managed by Home Assistant per installation. It is not controlled by sbam repository metadata.

### Start sbam

Start sbam after configuration is complete.

![chrome_5OngSH5IRc](https://github.com/atbore-phx/sbam/assets/11421185/9575b453-5132-4a24-9166-bc6d385690f1)

Check add-on logs for startup and runtime details.
