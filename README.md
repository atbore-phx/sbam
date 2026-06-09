[![codecov](https://codecov.io/gh/atbore-phx/sbam/graph/badge.svg?token=0fgSvHFiTx)](https://codecov.io/gh/atbore-phx/sbam)

# sbam - Smart Battery Advanced Manager.

Charge Fronius battery using SolCast weather forecast.

> [!CAUTION]
> This project is an unofficial, community-maintained implementation and is not sponsored, endorsed, or supported by Fronius. Use it at your own risk. The software and accompanying documentation are provided "AS IS", without any warranty, express or implied. Neither the author nor any contributor accepts liability for damages, losses, or other consequences resulting from the use of this project or its dependencies.

> [!IMPORTANT]
> Keep your GEN24 inverter firmware up to date. This integration is tested only on recent firmware versions and may not work correctly on older releases. We strongly recommend updating your inverter to the latest official firmware before using sbam. Always review Fronius release notes and back up your inverter settings before updating. Firmware versions prior to 1.34.6-1 are known to limit battery charging and are not supported.

## Introduction

After installing a Fronius Gen24 plus Solar system including a BYD battery, I wanted during months of low solar production to charge the battery at night when electricity costs are generally lower, in order to use the stored energy during the day.

Fronius through the local web interface reachable from the inverter's LAN IP provides the **Battery Management** utility -> **Time-dependent battery control**.

Indeed, it is possible to charge at night, but the process is static. Many times, I found the battery to be either too charged or too discharged the next day. I wanted something more advanced, dynamic, and adaptive that takes into account:

- weather forecasts
- daily electricity consumption related to my home
- the current battery charge
- the minimum reserve of the battery not to go below
- the time range when the energy operator offers cheaper electricity to force the charge.

NOTE:
In Solar.web, the energy balance does not display grid charge information to prevent customers from perceiving a higher consumption than actual. This is done to simplify the Solar.web view. (Source: Official Fronius support)

Here **sbam** is all this and much more :)

### Contributions
----
sbam is an open-source, community-driven project so contributions are welcome
from everyone. You can file issues to report bugs or request features,
improve documentation, add tests, or submit pull requests with proposed
changes. For larger features please open an issue first to discuss scope;
small fixes and documentation updates can be submitted directly as PRs.
When submitting code, include tests where applicable and keep changes focused so reviews are easy.

#### Support 💖

If you don't code but want to support the project, you can sponsor the project on GitHub Sponsors:

[![Sponsor on GitHub](https://img.shields.io/badge/Sponsor-GitHub-6f42c1?style=for-the-badge&logo=github)](https://github.com/sponsors/atbore-phx)

#### Vibe Coding

Optional Vibe Coding is accepted in this repo, prompt-driven workflow for contributors that can help generate `TASK` and `PLAN` documents and assist implementation. A set of prompts lives under [.github/prompts/](.github/prompts/) and guidance/examples are in [doc/vibe](docs/vibe). Feel free to improve the prompts and docs, but always validate generated code, run tests, and require human review before merging.

### Prerequisites
----
sbam requires the following prerequisites to function correctly: [link](docs/prereq.md)

### Home Assistant:
----
Sbam is available as an App (formerly known as add-on) for HAOS (Home Assistant OS).

**N.B. HAOS must be able to reach the Fronius inverter on its LAN IP.**

follow this guide to install and configure in HAOS: [link](home-assistant/addons/sbam/DOCS.md)

### MQTT Feed
----
sbam can publish schedule telemetry and accept control commands over MQTT when
`mqtt_enabled=true`.

For full setup, topic mapping, payload schemas, command examples, and migration
details, see [MQTT documentation](docs/mqtt.md).


### Stand Alone:
----
**sbam** can be run via cli with the following parameters:

```bash
sbam - Smart Battery Advanced Manager.
        Charge Fronius© battery using weather forecast.
        Initiate parameters from command line, env variables or config.yaml file.

Usage:
  sbam [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  configure   Configure Battery Storage Charge
  estimate    Estimate Forecast Solar Power
  help        Help about any command
  schedule    Schedule Battery Storage Charge

Flags:
  -h, --help      help for sbam
  -v, --version   version for sbam

Use "sbam [command] --help" for more information about a command.
```

#### Configure

Connect to the fronius inverter via modbus and set charging

```bash
Usage:
  sbam configure [flags]

Flags:
  -d, --defaults            set DEFAULTS
  -f, --force_charge        set FORCE_CHARGE
  -H, --fronius_ip string   set FRONIUS_IP
  -h, --help                help for configure
  -p, --power int           set percent of nominal POWER
```

#### Estimate

Print the solar forecast and the battery storage power

```bash
Usage:
  sbam estimate [flags]

Flags:
  -k, --apikey string              set APIKEY
  -f, --cache_file_prefix string   CACHE_FILE_NAME (default 'cached_forecast') (default "cached_forecast")
  -n, --cache_forecast             CACHE_FORECAST (default false)
  -l, --cache_time int32           CACHE_TIME (default 7200) (default 7200)
      --forecast_horizon string    Forecast horizon mode (default, next_solar_day, remaining_today, today, tomorrow, off) (default "default")
  -H, --fronius_ip string          set FRONIUS_IP
  -h, --help                       help for estimate
  -u, --url string                 Set the forecast URL. For multiple URLs, use a comma (,) to separate them
```

#### Schedule

Workflow to Check Forecast and Battery residual Capacity and decide if it has to be charged in a definited time range.

```bash
Usage:
  sbam schedule [flags]

Flags:
  -k, --apikey string                     APIKEY
  -E, --batt_reserve_end_hr string        BATT_RESERVE_END_HR (default END_HR)
  -S, --batt_reserve_start_hr string      BATT_RESERVE_START_HR (default START_HR)
  -f, --cache_file_prefix string          CACHE_FILE_PREFIX (default 'cached_forecast') (default "cached_forecast")
  -n, --cache_forecast                    CACHE_FORECAST (default false)
  -l, --cache_time int32                  CACHE_TIME (default 7200) (default 7200)
  -t, --crontab string                    CRONTAB (default "0 0 0 0 0")
      --consumption_horizon string        Consumption horizon mode (full_day, remaining_today) (default "full_day")
  -d, --defaults                          DEFAULTS (default true)
  -e, --end_hr string                     END_HR (default "00:55")
      --forecast_horizon string           Forecast horizon mode (default, next_solar_day, remaining_today, today, tomorrow, off) (default "default")
  -H, --fronius_ip string                 FRONIUS_IP
  -h, --help                              help for schedule
  -m, --max_charge float                  MAX_CHARGE (default 3500)
      --mqtt_broker string                MQTT broker URL
      --mqtt_client_id string             MQTT client identifier
      --mqtt_enabled                      Enable MQTT integration
      --mqtt_ha_discovery                 Enable Home Assistant MQTT discovery (default true)
      --mqtt_ha_discovery_prefix string   Home Assistant MQTT discovery prefix (default "homeassistant")
      --mqtt_password string              MQTT password
      --mqtt_tls_ca_file string           MQTT TLS CA certificate file
      --mqtt_tls_client_cert string       MQTT TLS client certificate file
      --mqtt_tls_client_cert_key string   MQTT TLS client key file
      --mqtt_tls_insecure_skip            Skip MQTT TLS certificate verification
      --mqtt_topic_prefix string          MQTT topic prefix (default "sbam")
      --mqtt_username string              MQTT username
  -r, --pw_batt_reserve float             PW_BATT_RESERVE
  -c, --pw_consumption float              PW_CONSUMPTION
  -L, --pw_lwt float                      PW_LWT
  -U, --pw_upt float                      PW_UPT
  -s, --start_hr string                   START_HR (default "00:00")
      --windows string                     Charge windows in YAML format (same as config.yaml windows: key)
  -u, --url string                        Set the Forecast URL. For multiple URLs, use a comma (,) to separate them
```

Charge windows can be defined either as a single legacy window (`--start_hr`,`--end_hr`,`--max_charge`) or as a multi-window list (`--windows` or the `windows:` key in `config.yaml`). The two forms are mutually exclusive.\

**Legacy single window:**\
Time windows can span midnight for both charge (`--start_hr`,`--end_hr`) and reserve (`--batt_reserve_start_hr`,`--batt_reserve_end_hr`) ranges.\
Example:\
`--start_hr 22:00 --end_hr 06:00` and optionally:\
`--batt_reserve_start_hr 23:00 --batt_reserve_end_hr 05:00` (if omitted defaults to the same as `start_hr`/`end_hr`).\

**Multi-window list (YAML):**\
The `windows:` key in `config.yaml` (or `--windows` flag / `WINDOWS` env var) accepts an ordered list of charge windows, each with its own `max_charge` and optional `forecast_horizon`/`consumption_horizon`. List order defines the daily sequence: the first entry starts first, the last entry ends last. Cross-midnight windows are supported.\
Example:\
`--windows '[{name: night, start: "02:00", end: "06:00", max_charge: 3500, forecast_horizon: tomorrow}, {name: midday, start: "12:00", end: "15:00", max_charge: 2000}]'`
Equal start/end values remain invalid.

Crontab syntax is supported to repeat programmatically the execution of the `schedule` command. By default, it is set to `0 0 0 0 0` which means that the command will be executed once at startup without any repetition or action waiting for mqtt commands (if mqtt is enabled).\
For eg. to run it every hour, set it to `0 * * * *`. For more information on crontab syntax, see [crontab.guru](https://crontab.guru/).

Forecast and consumption windows are controlled by two new options:

- `--forecast_horizon` / `FORECAST_HORIZON` / `forecast_horizon` — selects which
  forecast window to use (default: `default`):
  - `default` — current behavior: today before 12:00, tomorrow from 12:00 onward.
  - `next_solar_day` — same as `default`; targets the upcoming solar day.
  - `remaining_today` — only forecast intervals from now through end of local day.
  - `today` — full local calendar day.
  - `tomorrow` — full next local calendar day.
  - `off` — skip forecast retrieval entirely; only reserve charging is active.

- `--consumption_horizon` / `CONSUMPTION_HORIZON` / `consumption_horizon` — selects
  how the daily consumption value is applied (default: `full_day`):
  - `full_day` — use `pw_consumption` as-is (current behavior).
  - `remaining_today` — scale `pw_consumption` proportionally to the fraction of
    the local day remaining.

#### Debug Logs

To increase the log level to debug, just set the DEBUG environment variable to true.

```bash
export DEBUG=true
❯ bin/sbam --help
2026-04-29T11:15:19+02:00       DEBUG   Debug Logs activated: true
...
```

#### Log Format

sbam supports two log output formats: `console` (human-friendly) and `json` (machine-readable). The default is `console`.

When `LOG_TYPE=json` the logger emits JSON structured logs suitable for collectors.

Set the `LOG_TYPE` environment variable to choose the format:

```bash
export LOG_TYPE=json
export DEBUG=true
bin/sbam --help
{"level":"debug","ts":"2026-04-29T11:16:24+02:00","msg":"Debug Logs activated: true"}
...
```
The Home Assistant add-on exposes the same option as `log_type` in the add-on
Options (default: `console`).

#### Config file and env vars

A configuration file config.yaml and/or environment variables are also supported.

Configuration values are resolved with the following precedence (highest first):

1. CLI flag (e.g. `--url http://example/`)
2. Environment variable (e.g. `URL=http://example/`)
3. `config.yaml` in the working directory
4. Built-in default

