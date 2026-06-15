#!/usr/bin/with-contenv bashio

# Generate config.yaml from Supervisor options with MQTT auto-fill.
# JSON is valid YAML 1.2, so the Go app's viper reads it natively.
cd /data

# DEBUG and LOG_TYPE are read via os.Getenv (src/utils/log.go), not viper.
export DEBUG=$(bashio::config 'debug')
export LOG_TYPE=$(bashio::config 'log_type')

# MQTT autofill: dynamically discover broker details from Home Assistant
# services and merge them into /data/options.json.  Only keys that are
# empty in the user config are filled; existing values are preserved.
mqtt_autofill_from_ha_service() {
	local mqtt_enabled mqtt_broker
	mqtt_enabled=$(bashio::config 'mqtt_enabled')
	[ "${mqtt_enabled}" = "true" ] || { cp /data/options.json config.yaml; return 0; }
	mqtt_broker=$(bashio::config 'mqtt_optional_config.broker')
	[ -z "${mqtt_broker}" ] || { cp /data/options.json config.yaml; return 0; }
	bashio::services.available 'mqtt' || { cp /data/options.json config.yaml; return 0; }

	local mqtt_host mqtt_port mqtt_username mqtt_password filter
	mqtt_host=$(bashio::services 'mqtt' 'host')
	mqtt_port=$(bashio::services 'mqtt' 'port')
	mqtt_username=$(bashio::services 'mqtt' 'username')
	mqtt_password=$(bashio::services 'mqtt' 'password')

	filter="."
	if [ -n "${mqtt_host}" ] && [ -n "${mqtt_port}" ]; then
		filter="${filter} | .mqtt_optional_config.broker = \"tcp://${mqtt_host}:${mqtt_port}\""
	fi
	if [ -z "$(bashio::config 'mqtt_optional_config.username')" ]; then
		filter="${filter} | .mqtt_optional_config.username = \"${mqtt_username}\""
	fi
	if [ -z "$(bashio::config 'mqtt_optional_config.password')" ]; then
		filter="${filter} | .mqtt_optional_config.password = \"${mqtt_password}\""
	fi

	jq "${filter}" /data/options.json > config.yaml
}

mqtt_autofill_from_ha_service

# Reset inverter to defaults at startup if configured.
reset=$(bashio::config 'reset')
[ "$reset" = "true" ] && sbam configure -d

sbam schedule
