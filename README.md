# sbam - Smart Battery Advanced Manager.
[![codecov](https://codecov.io/gh/atbore-phx/sbam/graph/badge.svg?token=0fgSvHFiTx)](https://codecov.io/gh/atbore-phx/sbam)
## Intro

Charge Fronius battery using SolCast weather forecast.

``` bash
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

``` bash
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

``` bash
Usage:
  sbam estimate [flags]

Flags:
  -k, --apikey string       APIKEY
  -H, --fronius_ip string   FRONIUS_IP
  -h, --help                help for estimate
  -u, --url string          URL
```

## Schedule
Workflow to Check Forecast and Battery residual Capacity and decide if it has to be charged in a definited time range.

``` bash
Usage:
  sbam schedule [flags]

Flags:
  -k, --apikey string           APIKEY
  -t, --crontab string          crontab (default "0 0 0 0 0")
  -d, --defaults                DEFAULTS (default true)
  -e, --end_hr string           END_HR (default "05:55")
  -H, --fronius_ip string       FRONIUS_IP
  -h, --help                    help for schedule
  -m, --max_charge float        MAX_CHARGE (default 3500)
  -r, --pw_batt_reserve float   PW_BATT_RESERVE
  -c, --pw_consumption float    PW_CONSUMPTION
  -s, --start_hr string         START_HR (default "00:00")
  -u, --url string              URL

```