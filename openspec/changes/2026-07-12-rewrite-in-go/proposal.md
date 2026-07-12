## Why

The current `hs2mqtt` deployment in `homeserver/docker-compose.yaml` runs
`dersimn/hs100tomqtt:armhf` — a NodeJS bridge that has not been maintained
upstream, uses CLI-only configuration, and diverges structurally from the
sibling smart-home bridges in the same stack (`velux-mqtt-gw`,
`hue-to-mqtt-gw`, `miele-to-mqtt-gw`). Reimplementing it in Go — mirroring
those siblings — produces a single static binary, aligns configuration and
tooling (`go-logger`, `mqtt-gateway`, JSON config with `${ENV}` substitution,
GoReleaser, distroless image), and replaces the NodeJS runtime and its dead
dependency chain with a small hand-rolled TP-Link driver whose protocol
surface (XOR-obfuscated TCP `:9999` + JSON commands) is fully documented and
trivial to maintain.

## What Changes

- **BREAKING**: Complete rewrite of the application from NodeJS to Go. Node
  and the `tplink-smarthome-api` dependency are no longer required at
  runtime.
- **BREAKING**: Configuration format changes from CLI-flags / env vars to
  **JSON with `${ENV}` substitution**, matching the `hue-to-mqtt-gw` and
  `velux-mqtt-gw` template and reusing the shared `mqtt-gateway` config
  plumbing. The `docker-compose.yaml` service definition changes accordingly
  (config-file volume + `MQTT_PASSWORD` env).
- **BREAKING**: MQTT topic layout changes from the legacy
  `hs100/status/<deviceId>` / `hs100/set/<deviceId>` / `hs100/maintenance/…`
  scheme to the sibling style:
  - `{prefix}/bridge/state` — `online`/`offline` (retained, LWT)
  - `{prefix}/{name}/available` — `online`/`offline` (retained)
  - `{prefix}/{name}/state` — JSON state (retained)
  - `{prefix}/{name}/set` — command topic
  - `{prefix}/{name}/get` — republish current state on demand

  The `{name}` segment is a user-supplied config alias (like hue2mqtt's
  `names` map), not the opaque TP-Link `deviceId`. Home Assistant
  configuration is a one-time migration; there are only two plugs in the
  deployment.
- **NEW — hand-rolled TP-Link driver**: A standalone Go package (`tplink/`)
  implementing the well-documented TP-Link Smart-Plug protocol from scratch:
  TCP `:9999`, 4-byte length prefix, rolling-XOR obfuscation (initial key
  171), JSON payloads (`get_sysinfo`, `emeter.get_realtime`,
  `system.set_relay_state`). No external protocol dependency; matches the
  "own your driver" style of `velux/klf200` and `hue/hue`.
- **NEW — runtime HS100 vs HS110 detection**: On first successful poll,
  the driver inspects `sysinfo.feature` and treats the device as
  `has-emeter` iff the string contains the token `"ENE"` — the exact
  test used by the upstream `plasticrake/tplink-smarthome-api`. HS100
  plugs publish only `{"on": bool}`; HS110 plugs publish
  `{"on": bool, "power_w": …, "voltage_v": …, "current_a": …, "energy_kwh": …}`.
  Detection is per-device and cached; a re-detect happens on bridge
  restart. Subsequent polls of an HS110 batch `system.get_sysinfo` and
  `emeter.get_realtime` into one TCP round-trip.
- Replace NodeJS dependencies with Go equivalents:
  - `mqtt-smarthome-connect` + `yalm` → shared `mqtt-gateway` + `go-logger`
  - `tplink-smarthome-api` → in-repo `tplink/` package
  - `yetanothertimerlibrary` → `time.Ticker` per device goroutine
  - `yargs` (CLI + env) → JSON config loader with env-var substitution
- Static device list only. The current deployment uses a fixed IP list
  (`10.0.10.110 10.0.10.160`) — autodiscovery via UDP broadcast is out of
  scope. This matches the reality that `--net=host` is not used and the
  operator already knows the plug IPs.
- Per-device polling (default 3 s, configurable) via one goroutine per
  device, with reconnect on I/O errors and availability tracking published
  to MQTT.
- Bridge status via `mqtt-gateway` LWT (`online` on connect, `offline` on
  disconnect), matching sibling bridges.
- Add a Makefile, multi-stage distroless Dockerfile
  (`Dockerfile.goreleaser` + `Dockerfile.goreleaser-arm`), and a
  `.goreleaser.yml` mirroring `hue-to-mqtt-gw` / `velux-mqtt-gw`.
  Images publish to `ghcr.io/mqtt-home/hs100-to-mqtt-gw` (same
  registry/org convention as the sibling bridges).
- **NEW — Home Assistant MQTT auto-discovery**: The bridge publishes
  retained discovery configs under
  `homeassistant/switch/hs100_{name}/config` for each plug's relay and,
  when a device is detected as HS110, additional
  `homeassistant/sensor/hs100_{name}_{metric}/config` entries for power,
  voltage, current, and energy. All entities of a given plug share a
  single HA device block (`identifiers`, `name`, `model`, `manufacturer`)
  so they group as one device in HA. Discovery topics are cleared
  (empty retained payload) on graceful shutdown.

## Capabilities

### New Capabilities

- `tplink-driver`: The hand-rolled TP-Link Smart-Plug driver. Frames
  requests over TCP `:9999` with 4-byte big-endian length prefix and
  rolling-XOR obfuscation, serialises the JSON command envelopes
  (`system.get_sysinfo`, `emeter.get_realtime`, `system.set_relay_state`),
  parses responses, and exposes typed `SysInfo` / `EmeterRealtime` values.
  Includes the HS100 vs HS110 runtime feature-detection routine driven by
  `sysinfo.feature`.
- `device-manager`: Per-device lifecycle — one goroutine per configured
  device, ticker-driven polling of relay + emeter state, state-change
  detection, reconnect-on-error with backoff, and availability tracking
  (`online`/`offline`) exposed to the MQTT bridge.
- `mqtt-bridge`: Bidirectional MQTT — publishing retained state and
  availability to `{prefix}/{name}/state|available`, publishing the bridge
  status to `{prefix}/bridge/state`, and subscribing to `/set` and `/get`
  command topics. Command payloads accept `true`/`false`, `"ON"`/`"OFF"`,
  or `{"on": bool}` for compatibility with common MQTT-smarthome clients.
- `app-config`: JSON configuration loading with `${ENV}` substitution,
  documented defaults, and validation of required fields
  (`mqtt.url`, at least one `devices[]` entry with `host` + `name`).
- `app-runtime`: Application bootstrap and shutdown — config load, MQTT
  connect (blocks until connected), device manager start, signal handling,
  structured logging, pprof `:6060`.
- `build-and-deploy`: Makefile-driven `build`/`test`/`lint`/`run`,
  multi-stage distroless Docker images (`amd64` + `armhf`) published to
  `ghcr.io/mqtt-home/hs100-to-mqtt-gw`, and the `.goreleaser.yml` that
  produces both.
- `ha-discovery`: Home Assistant MQTT auto-discovery for each plug —
  one HA device per plug grouping a switch entity (relay) and, on
  HS110, four sensor entities (power W, voltage V, current A, energy
  kWh). Discovery topics are cleared on graceful shutdown.

### Modified Capabilities

<!-- None — openspec/specs/ is empty; this is the foundational change. -->

## Impact

- **Affected code**: There is no existing code in this repository yet
  (`openspec/` only). The change introduces the full Go tree
  (`app/main.go`, `app/config/`, `app/tplink/`, `app/bridge/`,
  `app/version/`), the Makefile, Dockerfiles, and `.goreleaser.yml`.
- **Deployment**: `homeserver/docker-compose.yaml` is updated to point
  `hs2mqtt` at `ghcr.io/mqtt-home/hs100-to-mqtt-gw:latest`, mount a
  `config.json`, and pass `MQTT_PASSWORD` via `environment:` rather
  than encoded in the URL.
- **Config**: New `config/hs2mqtt/config.json` on the homeserver — replaces
  the CLI-only configuration of the previous image.
- **MQTT contract**: Breaking change vs the current `hs100/status/<id>`
  layout. However, Home Assistant now picks up the plugs automatically
  via MQTT auto-discovery — existing hand-crafted entities in HA yaml
  that reference the old topics should be removed to avoid duplicates.
  Two plugs are affected.
- **Dependencies**: All NodeJS packages removed. New Go deps:
  `github.com/philipparndt/go-logger`, `github.com/philipparndt/mqtt-gateway`
  (which pulls `eclipse/paho.mqtt.golang`). No third-party TP-Link
  library.
- **Runtime**: No Node at runtime; single static binary; distroless
  image, meaningfully smaller than the current NodeJS image.
- **Documentation**: A new `README.md` covers Go build/run, JSON config,
  the new topic layout, and the HS100/HS110 payload difference.
