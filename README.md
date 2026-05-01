[![codecov](https://codecov.io/gh/atbore-phx/sbam/graph/badge.svg?token=0fgSvHFiTx)](https://codecov.io/gh/atbore-phx/sbam)

# sbam - Smart Battery Advanced Manager.

Charge Fronius battery using SolCast weather forecast.

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
Sbam is available as an add-on for HAOS (Home Assistant OS).

**N.B. HAOS must be able to reach the Fronius inverter on its LAN IP.**

follow this guide to install and configure in HAOS: [link](home-assistant/addons/sbam/DOCS.md)

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
  -d, --defaults            Set defaults
  -f, --force-charge        Force charge
  -H, --fronius_ip string   FRONIUS_IP
  -h, --help                help for configure
  -p, --power int16         Power (percent of nominal power)
```

#### Estimate

Print the solar forecast and the battery storage power

```bash
Usage:
  sbam estimate [flags]

Flags:
  -k, --apikey string       set APIKEY
  -H, --fronius_ip string   set FRONIUS_IP
  -h, --help                help for estimate
  -u, --url string          Set the forecast URL. For multiple URLs, use a comma (,) to separate them
  -n, --cache_forecast bool Enabling the cache forcast to reduce the number of times we query the forecast URL. Defaults to false
  -f, --cache_file_prefix   When caching is enabled, the forecast will be saved locally to files with this prefix. Defaults to cached_forecast
  -l, --cache_time          The length of time to cache the forecast. Defaults to 7200 seconds
```

#### Schedule

Workflow to Check Forecast and Battery residual Capacity and decide if it has to be charged in a definited time range.

```bash
Usage:
  sbam schedule [flags]

Flags:
  -k, --apikey string                  APIKEY
  -E, --batt_reserve_end_hr string     BATT_RESERVE_END_HR (default END_HR)
  -S, --batt_reserve_start_hr string   BATT_RESERVE_START_HR (default START_HR)
  -t, --crontab string                 CRONTAB (default "0 0 0 0 0")
  -d, --defaults                       DEFAULTS (default true)
  -e, --end_hr string                  END_HR (default "00:55")
  -H, --fronius_ip string              FRONIUS_IP
  -h, --help                           help for schedule
  -m, --max_charge float               MAX_CHARGE (default 3500)
  -r, --pw_batt_reserve float          PW_BATT_RESERVE
  -c, --pw_consumption float           PW_CONSUMPTION
  -s, --start_hr string                START_HR (default "00:00")
  -u, --url string                     Set the Forecast URL. For multiple URLs, use a comma (,) to separate them
  -n, --cache_forecast bool            Enabling the cache forcast to reduce the number of times we query the forecast URL. Defaults to false
  -f, --cache_file_prefix              When caching is enabled, the forecast will be saved locally to files with this prefix. Defaults to cached_forecast
  -l, --cache_time                     The length of time to cache the forecast. Defaults to 7200 seconds
```

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

