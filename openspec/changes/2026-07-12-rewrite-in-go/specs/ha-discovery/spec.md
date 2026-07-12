## ADDED Requirements

### Requirement: Switch discovery per plug

The system SHALL publish, retained, a Home Assistant MQTT discovery
config for each configured plug's relay under
`homeassistant/switch/hs100_{name}/config`, referencing the plug's
`{prefix}/{name}/state` topic for state, `{prefix}/{name}/set` for
command, and `{prefix}/{name}/available` for availability.

The switch payload SHALL include a `device` block carrying
`identifiers=["hs100_{name}"]`, `name`, `manufacturer="TP-Link"`,
`model` (from `sysinfo.model`), and `sw_version` (from
`sysinfo.sw_ver`), so all entities of the plug group under one HA
device.

The `value_template` SHALL be `{{ 'ON' if value_json.on else 'OFF' }}`,
with `payload_on="ON"` and `payload_off="OFF"`.

#### Scenario: Switch published

- **WHEN** a plug's first successful poll yields a valid `SysInfo`
- **THEN** a retained discovery payload is published to `homeassistant/switch/hs100_{name}/config`

#### Scenario: Shared device block

- **WHEN** an HS110 plug publishes its switch and sensor discovery configs
- **THEN** all five configs share identical `device.identifiers`, `device.name`, `device.model`, and `device.sw_version` values

### Requirement: Sensor discovery on HS110

The system SHALL, and only when a plug is detected as HS110
(has-emeter), additionally publish four retained sensor discovery
configs under
`homeassistant/sensor/hs100_{name}_{metric}/config` for `metric` in
`{power, voltage, current, energy}`, each with the units and classes
below:

| metric  | `unit_of_measurement` | `device_class` | `state_class`      | `value_template`             |
|---------|-----------------------|----------------|--------------------|------------------------------|
| power   | W                     | power          | measurement       | `{{ value_json.power_w }}`   |
| voltage | V                     | voltage        | measurement       | `{{ value_json.voltage_v }}` |
| current | A                     | current        | measurement       | `{{ value_json.current_a }}` |
| energy  | kWh                   | energy         | total_increasing  | `{{ value_json.energy_kwh }}`|

Each sensor SHALL share the same `device` block as the switch and use
the same `state_topic` (`{prefix}/{name}/state`) and
`availability_topic` (`{prefix}/{name}/available`).

#### Scenario: HS100 emits no sensors

- **WHEN** a plug is detected as HS100 (no emeter feature)
- **THEN** no `homeassistant/sensor/hs100_{name}_*/config` topic is published

#### Scenario: HS110 emits four sensors

- **WHEN** a plug is detected as HS110
- **THEN** exactly four sensor discovery topics are published: `power`, `voltage`, `current`, `energy`

### Requirement: Discovery timing

The system SHALL delay publishing discovery configs for a device until
the first successful poll that resolves both `sysinfo.model`/`sw_ver`
and the `has-emeter` flag, so that HA never sees a switch with unknown
model or a phantom sensor for a plug whose type has not yet been
determined.

#### Scenario: Deferred until known

- **WHEN** a device is unreachable at startup and takes 20 s to respond
- **THEN** no discovery topics are published for that device until the first successful poll
- **AND** once the poll succeeds, discovery is published within one tick of the manager

### Requirement: Discovery idempotency

Publishing discovery configs SHALL be idempotent. Re-invoking the
publisher for the same device (e.g. after a re-detect on reconnect)
SHALL emit the same topic set with the same payload set, and SHALL NOT
grow the tracked-topic list.

#### Scenario: Re-publish on reconnect

- **WHEN** the discovery publisher is invoked twice for the same HS110 device
- **THEN** the tracked-topic set still contains exactly five entries

### Requirement: Discovery cleanup on graceful shutdown

The system SHALL clear every discovery topic it has published by
publishing an empty retained payload to each on graceful shutdown
(`SIGINT`/`SIGTERM`), before the MQTT client disconnects, so that HA
removes the entities.

#### Scenario: Cleanup on shutdown

- **WHEN** the application shuts down gracefully after publishing discovery for two plugs (one HS100, one HS110)
- **THEN** six empty retained payloads are published in total (1 switch + 0 sensors + 1 switch + 4 sensors) to the previously tracked discovery topics

#### Scenario: No cleanup on crash

- **WHEN** the process is killed by SIGKILL
- **THEN** discovery topics remain (retained), and HA still sees the entities as available on next start (bridge availability handles online/offline)

### Requirement: Object-id namespacing

Discovery object-ids SHALL be prefixed with `hs100_` so that entities
never collide with entities published by sibling bridges
(`velux-mqtt-gw`, `hue-to-mqtt-gw`, etc.) in the same Home Assistant
instance.

#### Scenario: Prefix present

- **WHEN** a plug is named `office`
- **THEN** its discovery topics are `homeassistant/switch/hs100_office/config` and (if HS110) `homeassistant/sensor/hs100_office_{power,voltage,current,energy}/config`
