# hs100-to-mqtt-gw — TP-Link Smart Plug to MQTT Bridge (Go)

[![mqtt-smarthome](https://img.shields.io/badge/mqtt-smarthome-blue.svg)](https://github.com/mqtt-smarthome/mqtt-smarthome)

A small, static Go bridge that polls TP-Link HS100 and HS110 Smart Plugs
over the classic XOR-obfuscated TCP `:9999` protocol and mirrors their state
onto MQTT. Includes Home Assistant MQTT auto-discovery so each plug appears
automatically — a switch entity for the relay, and (on HS110) four
measurement sensors.

## Why not `dersimn/hs100tomqtt`?

The previous deployment used `dersimn/hs100tomqtt`, a NodeJS bridge whose
upstream `tplink-smarthome-api` dependency has gone unmaintained. It has no
config file (CLI flags only), publishes to opaque `hs100/status/<deviceId>`
topics driven by TP-Link's internal device IDs, and offers no Home Assistant
auto-discovery. This rewrite drops the NodeJS runtime, uses a JSON config
with `${ENV}` substitution, publishes to human-readable
`{prefix}/{name}/...` topics, and hands Home Assistant everything it needs
on first poll.

## Supported devices

- **HS100** — relay only.
- **HS110** — relay plus emeter (power, voltage, current, cumulative energy).

Runtime feature detection reads `sysinfo.feature` from each plug on the
first successful poll: the device is treated as `has-emeter` iff the
feature string contains the token `ENE`. This is the same test used by
the upstream `plasticrake/tplink-smarthome-api`. Detection is cached for
the lifetime of the process; a bridge restart re-detects.

**Explicit non-goals:**

- Multi-outlet strips (HS107, HS300 — they carry a `children[]` array and
  require per-outlet `context.child_ids`).
- Kasa / Tapo plugs that use the newer KLAP or AES authentication
  (incompatible with the classic XOR scheme).

## MQTT topic layout

`{prefix}` defaults to `hs100` and comes from `mqtt.topic` in the config.
`{name}` is the user-supplied alias from the `devices[]` entry.

| Topic                              | Direction | Retained | Description                                                      |
|------------------------------------|-----------|----------|------------------------------------------------------------------|
| `{prefix}/bridge/state`            | publish   | yes      | `online` on connect, `offline` via LWT on disconnect             |
| `{prefix}/{name}/available`        | publish   | yes      | `online` / `offline` — per-device reachability                   |
| `{prefix}/{name}/state`            | publish   | yes      | JSON state payload (see below)                                   |
| `{prefix}/{name}/set`              | subscribe | —        | Command topic — turn the relay on / off                          |
| `{prefix}/{name}/get`              | subscribe | —        | Trigger a re-publish of the last cached `/state` payload         |

## State payload — HS100 vs HS110

The published JSON on `{prefix}/{name}/state` reflects what the device
actually supports. HS100 plugs publish the boolean relay state only; HS110
plugs add four numeric fields with self-documenting SI-unit suffixes.

**HS100 (no emeter):**

```json
{"on": true}
```

**HS110 (with emeter):**

```json
{
  "on": true,
  "power_w": 42.1,
  "voltage_v": 230.5,
  "current_a": 0.183,
  "energy_kwh": 12.4
}
```

The four numeric fields are omitted entirely for HS100 (they're
`omitempty`), so a downstream consumer can safely branch on presence.

## `/set` command payloads

Three forms are accepted on `{prefix}/{name}/set`. Anything else is logged
at `error` level and dropped without changing the plug state.

| Form            | Example                    | Effect                     |
|-----------------|----------------------------|----------------------------|
| Raw JSON bool   | `true` / `false`           | Set relay on / off         |
| String          | `ON` / `OFF` (any case, quoted or bare) | Set relay on / off |
| Object          | `{"on": true}` / `{"on": false}` | Set relay on / off   |

After a successful `/set` the bridge immediately re-polls the device and
publishes the new state — no need to wait for the next poll tick.

## Home Assistant auto-discovery

Every configured plug is announced to Home Assistant automatically on its
first successful poll (once the model and HS100/HS110 capability are known).
All entities for one plug share a single HA device block, so they group
into a single card in HA.

- Always: one **switch** entity for the relay.
- HS110 only: four **sensor** entities — `power_w`, `voltage_v`,
  `current_a`, `energy_kwh` (with proper `device_class` and
  `state_class` metadata).

Discovery topics use the `hs100_` object-id prefix so entities never
collide with other bridges' entities:

```
homeassistant/switch/hs100_{name}/config
homeassistant/sensor/hs100_{name}_power/config     (HS110 only)
homeassistant/sensor/hs100_{name}_voltage/config   (HS110 only)
homeassistant/sensor/hs100_{name}_current/config   (HS110 only)
homeassistant/sensor/hs100_{name}_energy/config    (HS110 only)
```

On graceful shutdown (`SIGINT` / `SIGTERM`) the bridge publishes an empty
retained payload to every discovery topic it created, so Home Assistant
removes the entities cleanly.

## Configuration

The bridge takes exactly one command-line argument: the path to a JSON
config file. A ready-to-copy example lives at
[`production/config/config-example.json`](production/config/config-example.json):

```json
{
    "mqtt": {
        "url": "tcp://mosquitto:1883",
        "username": "mosquitto",
        "password": "${MQTT_PASSWORD}",
        "topic": "hs100",
        "retain": true,
        "qos": 1
    },
    "polling-interval-seconds": 3,
    "devices": [
        { "host": "10.0.10.110", "name": "device-a" },
        { "host": "10.0.10.160", "name": "device-b" }
    ],
    "loglevel": "info"
}
```

### Field reference

| Field                        | Type    | Default  | Description                                                       |
|------------------------------|---------|----------|-------------------------------------------------------------------|
| `mqtt.url`                   | string  | —        | Required. Broker URL, e.g. `tcp://host:1883` or `ssl://host:8883` |
| `mqtt.username`              | string  | `""`     | Broker login (optional)                                           |
| `mqtt.password`              | string  | `""`     | Broker password (optional; `${ENV}` substitution supported)       |
| `mqtt.topic`                 | string  | `hs100`  | Base topic prefix for all bridge topics                           |
| `mqtt.retain`                | bool    | `true`   | Retain flag on published state / availability / bridge messages   |
| `mqtt.qos`                   | integer | `1`      | MQTT QoS level for publishes (0, 1, or 2)                         |
| `mqtt.client-id`             | string  | `""`     | Optional MQTT client-id prefix                                    |
| `devices[].host`             | string  | —        | Required. IP or hostname of the plug on the LAN                   |
| `devices[].name`             | string  | —        | Required. Alias used in topics; must be unique and MQTT-safe (no `/`, `#`, `+`) |
| `polling-interval-seconds`   | integer | `3`      | Poll interval per device                                          |
| `loglevel`                   | string  | `info`   | `debug`, `info`, `warn`, or `error`                               |

## Environment variable substitution

Any `${NAME}` placeholder in the JSON is replaced with the value of the
matching process environment variable before parsing (via the shared
`mqtt-gateway` config loader). Missing variables are substituted with an
empty string.

Typical use — keep the broker password out of the file:

```json
{ "mqtt": { "password": "${MQTT_PASSWORD}" } }
```

```bash
MQTT_PASSWORD=s3cr3t ./hs2mqtt /var/lib/hs2mqtt/config.json
```

## Docker (recommended)

Images are published to `ghcr.io/mqtt-home/hs100-to-mqtt-gw` for `amd64`
and `linux/arm/v7`. Drop this fragment into your `docker-compose.yaml`:

```yaml
hs2mqtt:
  image: ghcr.io/mqtt-home/hs100-to-mqtt-gw:latest
  container_name: hs2mqtt
  volumes:
    - ./config/hs2mqtt/config.json:/var/lib/hs2mqtt/config.json:ro
  environment:
    - MQTT_PASSWORD=your-broker-password
  networks: [mosquitto]
  restart: always
```

The image's default command is `/hs2mqtt /var/lib/hs2mqtt/config.json`, so
mounting the config file at that path is all that's required. `--net=host`
is intentionally not used — the operator supplies a static IP list in the
config file.

A copy-pasteable deployment fragment and Home Assistant migration notes
are provided in [`production/docker-compose-hs2mqtt.yaml`](production/docker-compose-hs2mqtt.yaml)
and [`production/HA-MIGRATION.md`](production/HA-MIGRATION.md).

## Build from source

Requirements: Go 1.25+ and `make`.

```bash
cd app
make build      # produces ./hs2mqtt
make test       # run unit tests
make lint       # run golangci-lint

./hs2mqtt ../production/config/config-example.json
```

## Diagnostics

The bridge exposes standard Go pprof and expvar endpoints on `:6060`
(mirroring the sibling bridges). Useful for the occasional goroutine or
heap inspection:

```
http://<host>:6060/debug/pprof/
http://<host>:6060/debug/vars
```

## Credits / design source of truth

Package layout, config format, MQTT plumbing (`mqtt-gateway`), logging
(`go-logger`), and build/release tooling all follow the same shape as the
sibling bridges [`hue-to-mqtt-gw`](https://github.com/mqtt-home/hue-to-mqtt-gw)
and [`velux-mqtt-gw`](https://github.com/mqtt-home/velux-mqtt-gw). Refer to
either of those repos for the shared conventions used across this stack.
