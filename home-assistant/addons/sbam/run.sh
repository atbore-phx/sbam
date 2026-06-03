#!/usr/bin/with-contenv bashio

export URL=$(bashio::config 'url')
export APIKEY=$(bashio::config 'apikey')
export FRONIUS_IP=$(bashio::config 'fronius_ip')
export START_HR=$(bashio::config 'start_hr')
export END_HR=$(bashio::config 'end_hr')
export CRONTAB=$(bashio::config 'crontab')
export PW_CONSUMPTION=$(bashio::config 'pw_consumption')
export MAX_CHARGE=$(bashio::config 'max_charge')
export PW_LWT=$(bashio::config 'pw_lwt')
export PW_UPT=$(bashio::config 'pw_upt')
export PW_BATT_RESERVE=$(bashio::config 'pw_batt_reserve')
export BATT_RESERVE_START_HR=$(bashio::config 'batt_reserve_start_hr')
export BATT_RESERVE_END_HR=$(bashio::config 'batt_reserve_end_hr')
export DEFAULTS=$(bashio::config 'defaults')
export RESET=$(bashio::config 'reset')
export DEBUG=$(bashio::config 'debug')
export LOG_TYPE=$(bashio::config 'log_type')
export CACHE_FORECAST=$(bashio::config 'cache_forecast')
export CACHE_FILE_PREFIX=$(bashio::config 'cache_file_prefix')
export CACHE_TIME=$(bashio::config 'cache_time')
export MQTT_ENABLED=$(bashio::config 'mqtt_enabled')
export MQTT_BROKER=$(bashio::config 'mqtt_broker')
export MQTT_CLIENT_ID=$(bashio::config 'mqtt_client_id')
export MQTT_USERNAME=$(bashio::config 'mqtt_username')
export MQTT_PASSWORD=$(bashio::config 'mqtt_password')
export MQTT_TOPIC_PREFIX=$(bashio::config 'mqtt_topic_prefix')
export MQTT_HA_DISCOVERY=$(bashio::config 'mqtt_ha_discovery')
export MQTT_HA_DISCOVERY_PREFIX=$(bashio::config 'mqtt_ha_discovery_prefix')
export FORECAST_HORIZON=$(bashio::config 'forecast_horizon')
export CONSUMPTION_HORIZON=$(bashio::config 'consumption_horizon')
export WINDOWS_JSON=$(bashio::config 'windows' | jq -c '.')

mqtt_autofill_from_ha_service() {
	[ "${MQTT_ENABLED}" = "true" ] || return 0
	[ -z "${MQTT_BROKER}" ] || return 0
	bashio::services.available 'mqtt' || return 0

	local mqtt_host mqtt_port mqtt_username mqtt_password
	mqtt_host=$(bashio::services 'mqtt' 'host')
	mqtt_port=$(bashio::services 'mqtt' 'port')
	mqtt_username=$(bashio::services 'mqtt' 'username')
	mqtt_password=$(bashio::services 'mqtt' 'password')

	if [ -n "${mqtt_host}" ] && [ -n "${mqtt_port}" ]; then
		export MQTT_BROKER="tcp://${mqtt_host}:${mqtt_port}"
	fi
	[ -n "${MQTT_USERNAME}" ] || export MQTT_USERNAME="${mqtt_username}"
	[ -n "${MQTT_PASSWORD}" ] || export MQTT_PASSWORD="${mqtt_password}"
}

mqtt_autofill_from_ha_service

[ "$RESET" = "true" ] && sbam configure -d

sbam schedule
