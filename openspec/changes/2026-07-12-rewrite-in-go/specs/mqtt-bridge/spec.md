## ADDED Requirements

### Requirement: MQTT connection lifecycle

The system SHALL connect to the configured MQTT broker on startup with
the optional username/password, retry on failure via `mqtt-gateway`, and
disconnect cleanly on shutdown.

#### Scenario: Successful connect

- **WHEN** the application starts with a valid broker URL and credentials
- **THEN** the MQTT client connects and is ready to publish and subscribe

#### Scenario: Retry on failure

- **WHEN** the initial MQTT connection attempt fails
- **THEN** the system retries with backoff before giving up

### Requirement: Bridge status

The system SHALL publish `{prefix}/bridge/state` as `online` (retained)
once MQTT is connected, and SHALL register a last-will message of
`offline` (retained) on the same topic so that an unclean exit surfaces
as bridge offline to subscribers.

#### Scenario: Bridge online

- **WHEN** the MQTT connection is established
- **THEN** `{prefix}/bridge/state` is published as `online` (retained)

#### Scenario: Bridge offline via LWT

- **WHEN** the process crashes or the container is killed
- **THEN** the broker delivers the last-will `offline` on `{prefix}/bridge/state`

### Requirement: Device state publishing

The system SHALL publish, retained, each device's state to
`{prefix}/{name}/state` as a JSON object.

For an HS100 (no emeter) the payload SHALL be `{"on": bool}`.

For an HS110 (with emeter) the payload SHALL be
`{"on": bool, "power_w": number, "voltage_v": number, "current_a": number, "energy_kwh": number}`.

#### Scenario: HS100 payload

- **WHEN** a device is detected as HS100 and its relay is on
- **THEN** the published payload equals `{"on": true}`

#### Scenario: HS110 payload

- **WHEN** a device is detected as HS110, its relay is on, and emeter reports 42.1 W, 230.5 V, 0.183 A, 12.4 kWh
- **THEN** the published payload equals `{"on": true, "power_w": 42.1, "voltage_v": 230.5, "current_a": 0.183, "energy_kwh": 12.4}`

### Requirement: Command subscription

The system SHALL subscribe to `{prefix}/+/set` and `{prefix}/+/get` and
route each incoming message by the `{name}` segment to the corresponding
device.

The `/set` payload SHALL be accepted in three forms — raw JSON boolean
(`true`/`false`), string (`"ON"`/`"OFF"` case-insensitive), and object
(`{"on": bool}`) — and any other payload SHALL be logged at `error` and
ignored.

The `/get` topic SHALL re-publish the currently cached state payload for
that device regardless of change detection.

#### Scenario: Boolean set

- **WHEN** `true` is received on `{prefix}/device-a/set`
- **THEN** the driver invokes `SetRelay(ctx, true)` on device-a
- **AND** the next poll's state is published immediately

#### Scenario: String set

- **WHEN** `"OFF"` is received on `{prefix}/device-a/set`
- **THEN** the driver invokes `SetRelay(ctx, false)`

#### Scenario: Object set

- **WHEN** `{"on": true}` is received on `{prefix}/device-a/set`
- **THEN** the driver invokes `SetRelay(ctx, true)`

#### Scenario: Get

- **WHEN** any payload is received on `{prefix}/device-a/get`
- **THEN** the cached state payload for device-a is re-published to `{prefix}/device-a/state`

#### Scenario: Unknown device

- **WHEN** a message arrives on `{prefix}/unknown/set`
- **THEN** the message is logged at `debug` and no driver call is made

#### Scenario: Invalid payload

- **WHEN** the payload on `/set` cannot be interpreted as a boolean
- **THEN** the system logs an error and takes no device action
