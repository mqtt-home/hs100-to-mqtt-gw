## ADDED Requirements

### Requirement: JSON config with env substitution

The system SHALL load its configuration from a JSON file whose path is
given as the sole command-line argument, SHALL substitute `${NAME}`
placeholders with the values of the corresponding environment variables
(via `mqtt-gateway/config.ReplaceEnvVariables`) before unmarshalling,
and SHALL fail startup with a non-zero exit code and a descriptive log
line if the file cannot be read or parsed.

#### Scenario: Env substitution

- **WHEN** the config contains `"password": "${MQTT_PASSWORD}"` and `MQTT_PASSWORD=secret` is exported
- **THEN** the parsed config's password field equals `"secret"`

#### Scenario: Missing file

- **WHEN** the config path does not exist
- **THEN** the process exits with a non-zero code and logs the read error

### Requirement: Defaults

The system SHALL apply the following defaults for unset optional
fields: `mqtt.retain=true`, `mqtt.qos=1`, `mqtt.topic="hs100"`,
`polling-interval-seconds=3`, `loglevel="info"`. Boolean defaults that
are `true` SHALL be pre-seeded before unmarshalling so that an explicit
`false` in the file overrides them.

#### Scenario: Missing optional fields

- **WHEN** the config file omits `mqtt.qos`, `mqtt.topic`, and `polling-interval-seconds`
- **THEN** the loaded config exposes `qos=1`, `topic="hs100"`, and `PollingIntervalSeconds=3`

#### Scenario: Explicit false retained

- **WHEN** the config file contains `"retain": false`
- **THEN** the loaded config exposes `retain=false`

### Requirement: Validation of required fields

The system SHALL validate on load that `mqtt.url` is non-empty and that
`devices` contains at least one entry; each entry SHALL have a
non-empty `host` and a non-empty `name`; `name` SHALL be unique across
devices; and `name` SHALL NOT contain any of `/`, `#`, `+` (MQTT
wildcards / separator). Any violation SHALL cause `LoadConfig` to
return an error and the process to exit non-zero.

#### Scenario: Missing mqtt.url

- **WHEN** `mqtt.url` is empty
- **THEN** `LoadConfig` returns an error naming that field

#### Scenario: Empty devices

- **WHEN** `devices` is `[]` or absent
- **THEN** `LoadConfig` returns an error

#### Scenario: Duplicate names

- **WHEN** two devices share the same `name`
- **THEN** `LoadConfig` returns an error naming the duplicate

#### Scenario: Wildcard in name

- **WHEN** a device name contains `#`, `+`, or `/`
- **THEN** `LoadConfig` returns an error naming the offending device

### Requirement: MQTT gateway config adapter

The system SHALL expose `MQTTConfig.ToGatewayConfig()` returning the
shared `mqtt-gateway/config.MQTTConfig` type, carrying `URL`, `Retain`,
`Topic`, `QoS`, `Username`, and `Password`.

#### Scenario: Round trip

- **WHEN** `ToGatewayConfig` is called
- **THEN** every field present in the local `MQTTConfig` appears in the returned gateway config with the same value
