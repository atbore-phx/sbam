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

### Prerequisites

sbam requires the following prerequisites to function correctly: [link](docs/prereq.md)

### Home Assistant:

Sbam is available as an add-on for HAOS (Home Assistant OS).

**N.B. HAOS must be able to reach the Fronius inverter on its LAN IP.**

follow this guide to install and configure in HAOS: [link](home-assistant/addons/sbam/DOCS.md)

### Stand Alone:

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

## Configure

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

## Estimate

Print the solar forecast and the battery storage power

```bash
Usage:
  sbam estimate [flags]

Flags:
  -k, --apikey string       set APIKEY
  -H, --fronius_ip string   set FRONIUS_IP
  -h, --help                help for estimate
  -u, --url string          Set the URL. For multiple URLs, use a comma (,) to separate them
```

## Schedule

Workflow to Check Forecast and Battery residual Capacity and decide if it has to be charged in a definited time range.

```bash
Usage:
  sbam schedule [flags]

Flags:
  -k, --apikey string           APIKEY
  -t, --crontab string          CRONTAB (default "0 0 0 0 0")
  -d, --defaults                DEFAULTS (default true)
  -e, --end_hr string           END_HR (default "05:55")
  -H, --fronius_ip string       FRONIUS_IP
  -h, --help                    help for schedule
  -m, --max_charge float        MAX_CHARGE (default 3500)
  -r, --pw_batt_reserve float   PW_BATT_RESERVE
  -c, --pw_consumption float    PW_CONSUMPTION
  -s, --start_hr string         START_HR (default "00:00")
  -u, --url string              Set the URL. For multiple URLs, use a comma (,) to separate them
```

## Debug Logs

To increase the log level to debug, just set the DEBUG environment variable to true.

```bash
export DEBUG=true
❯ bin/sbam --help
{"level":"debug","ts":"2024-11-17T12:16:28+01:00","msg":"Debug Logs activated: true"}
...
```

## Config file and env vars

A configuration file config.yml and/or environment variables are also supported.
