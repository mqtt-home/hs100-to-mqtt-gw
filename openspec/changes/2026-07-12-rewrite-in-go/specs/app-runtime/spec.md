## ADDED Requirements

### Requirement: Bootstrap

On startup, the system SHALL, in order: initialise the logger at level
`info`, log a version banner (`version`, `commit`, `built`), require
exactly one command-line argument (the config path) or exit non-zero,
load the config, apply the config-supplied log level, start the pprof
HTTP listener on `:6060`, connect to MQTT (blocking until connected),
publish the bridge status, start the device manager, and then block on
`SIGINT`/`SIGTERM`.

#### Scenario: No argument

- **WHEN** the binary is invoked with no arguments
- **THEN** the process exits non-zero and logs "Expected config file as argument"

#### Scenario: Successful start

- **WHEN** the binary is invoked with a valid config file
- **THEN** it connects MQTT, publishes bridge `online`, launches device goroutines, and logs "Application is now ready"

### Requirement: Structured logging

The system SHALL use `github.com/philipparndt/go-logger` for all log
output, SHALL emit structured key/value fields (not printf-style), and
SHALL honour the config `loglevel` (`error`, `warn`, `info`, `debug`,
`trace`).

#### Scenario: Debug level

- **WHEN** `loglevel` is `debug` and a device is polled
- **THEN** a debug log line records the device name and the outcome

### Requirement: Graceful shutdown

On `SIGINT` or `SIGTERM`, the system SHALL cancel the root context so
that all device goroutines return within one polling interval, and
SHALL then exit with status 0.

#### Scenario: SIGTERM

- **WHEN** the process receives SIGTERM
- **THEN** the manager stops all device goroutines and the process exits with status 0

### Requirement: Diagnostics endpoint

The system SHALL expose `expvar` counters and `net/http/pprof`
endpoints on `:6060` for operator diagnostics, matching the sibling
bridges.

#### Scenario: pprof reachable

- **WHEN** the operator issues an HTTP GET to `http://<host>:6060/debug/pprof/`
- **THEN** the pprof index is returned
