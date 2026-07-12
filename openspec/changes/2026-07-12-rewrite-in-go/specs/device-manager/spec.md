## ADDED Requirements

### Requirement: Per-device goroutine

The system SHALL run one polling goroutine per configured device,
spawned from `Manager.Run(ctx)` and torn down when the context is
cancelled.

#### Scenario: Fan out

- **WHEN** the configuration contains N devices
- **THEN** N independent goroutines are running after `Manager.Run`
- **AND** each goroutine polls its own device without blocking the others

#### Scenario: Shutdown

- **WHEN** the parent context is cancelled
- **THEN** every device goroutine returns within one polling interval

### Requirement: Polling cadence

The system SHALL poll each device at the configured
`polling-interval-seconds` (default 3) using a `time.Ticker`, and SHALL
publish the resulting state to the MQTT bridge on every successful
poll where the JSON payload differs from the previously published value
for that device.

#### Scenario: Steady state

- **WHEN** a plug's relay state and emeter readings are unchanged between two polls
- **THEN** no MQTT publish is issued for the `state` topic

#### Scenario: State change

- **WHEN** the relay is toggled by an external actor between two polls
- **THEN** the next poll publishes the new state

### Requirement: HS100/HS110 runtime detection

The system SHALL determine whether a device supports emeter readings by
inspecting the `feature` field of the first successful `GetSysInfo`
response per connection lifetime, caching the result on the device
struct, and using it to decide whether subsequent polls also call
`GetEmeter`.

#### Scenario: HS100 device

- **WHEN** the first sysinfo response for a device has `feature == "TIM"`
- **THEN** `HasEmeter` is set to `false`
- **AND** subsequent polls skip `GetEmeter`
- **AND** the published state contains only `{"on": bool}`

#### Scenario: HS110 device

- **WHEN** the first sysinfo response for a device has `feature == "TIM:ENE"`
- **THEN** `HasEmeter` is set to `true`
- **AND** subsequent polls also call `GetEmeter`
- **AND** the published state contains `on`, `power_w`, `voltage_v`, `current_a`, `energy_kwh`

#### Scenario: Physical device replacement

- **WHEN** the bridge is restarted after a plug at a given IP is swapped for a different model
- **THEN** detection re-runs on the first successful poll and `HasEmeter` is set to the new model's capability

### Requirement: Availability tracking

The system SHALL track availability per device and publish
`{prefix}/{name}/available` as `online` on the first successful poll
after startup or after any failure, and as `offline` on the first
polling failure after startup or after any success.

#### Scenario: Startup

- **WHEN** the first poll of a device succeeds
- **THEN** `{prefix}/{name}/available` is published retained as `online`

#### Scenario: Failure transition

- **WHEN** a poll fails after a previous success
- **THEN** `{prefix}/{name}/available` is published retained as `offline`
- **AND** further consecutive failures do NOT re-publish

#### Scenario: Recovery transition

- **WHEN** a poll succeeds after a previous failure
- **THEN** `{prefix}/{name}/available` is published retained as `online`

### Requirement: Reconnect backoff

The system SHALL apply exponential backoff to consecutive polling
failures, starting at 1 second and doubling up to a maximum of 60
seconds, and SHALL reset the backoff to the base interval on the next
successful poll.

#### Scenario: Escalation

- **WHEN** five consecutive polls fail
- **THEN** the intervals between attempts are 1s, 2s, 4s, 8s, 16s

#### Scenario: Reset on success

- **WHEN** a failure sequence is followed by a successful poll
- **THEN** subsequent poll cadence returns to the configured interval
