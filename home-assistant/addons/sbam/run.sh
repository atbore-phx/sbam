#!/usr/bin/with-contenv bashio

# Generate config.yaml from Supervisor options.
# JSON is valid YAML 1.2, so the Go app's viper reads it natively.
cd /data
cp /data/options.json config.yaml

# DEBUG and LOG_TYPE are read via os.Getenv (src/utils/log.go), not viper.
export DEBUG=$(bashio::config 'debug')
export LOG_TYPE=$(bashio::config 'log_type')

# MQTT autofill: dynamically discover broker details from Home Assistant services.
# Exported env vars override config.yaml values via viper's AutomaticEnv().
mqtt_autofill_from_ha_service() {
	local mqtt_enabled mqtt_broker mqtt_user mqtt_yaml
	mqtt_enabled=$(bashio::config 'mqtt_enabled')
	[ "${mqtt_enabled}" = "true" ] || return 0
	mqtt_broker=$(bashio::config 'mqtt_optional_config.broker')
	[ -z "${mqtt_broker}" ] || return 0
	bashio::services.available 'mqtt' || return 0

	local mqtt_host mqtt_port mqtt_username mqtt_password
	mqtt_host=$(bashio::services 'mqtt' 'host')
	mqtt_port=$(bashio::services 'mqtt' 'port')
	mqtt_username=$(bashio::services 'mqtt' 'username')
	mqtt_password=$(bashio::services 'mqtt' 'password')

	if [ -n "${mqtt_host}" ] && [ -n "${mqtt_port}" ]; then
		mqtt_yaml="broker: tcp://${mqtt_host}:${mqtt_port}"
	fi
	mqtt_user=$(bashio::config 'mqtt_optional_config.username')
	if [ -z "${mqtt_user}" ]; then
		mqtt_yaml="${mqtt_yaml}${mqtt_yaml:+
}username: ${mqtt_username}"
	fi
	if [ -z "$(bashio::config 'mqtt_optional_config.password')" ]; then
		mqtt_yaml="${mqtt_yaml}${mqtt_yaml:+
}password: ${mqtt_password}"
	fi
	[ -n "${mqtt_yaml}" ] && export MQTT_OPTIONAL_CONFIG="${mqtt_yaml}"
}

mqtt_autofill_from_ha_service

# Reset inverter to defaults at startup if configured.
reset=$(bashio::config 'reset')
[ "$reset" = "true" ] && sbam configure -d

sbam schedule
