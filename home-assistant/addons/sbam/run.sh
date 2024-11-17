#!/usr/bin/with-contenv bashio

export URL=$(bashio::config 'url')
export APIKEY=$(bashio::config 'apikey')
export FRONIUS_IP=$(bashio::config 'fronius_ip')
export START_HR=$(bashio::config 'start_hr')
export END_HR=$(bashio::config 'end_hr')
export CRONTAB=$(bashio::config 'crontab')
export PW_CONSUMPTION=$(bashio::config 'pw_consumption')
export MAX_CHARGE=$(bashio::config 'max_charge')
export PW_BATT_RESERVE=$(bashio::config 'pw_batt_reserve')
export DEFAULTS=$(bashio::config 'defaults')
export RESET=$(bashio::config 'reset')
export DEBUG=$(bashio::config 'debug')

[ "$RESET" = "true" ] && sbam configure -d

sbam schedule
