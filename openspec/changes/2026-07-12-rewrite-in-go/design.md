## Context

The current `hs2mqtt` service is `dersimn/hs100tomqtt:armhf` (NodeJS,
`tplink-smarthome-api`), driven purely by CLI flags:

```
command: -m mqtt://mosquitto:UR23x2jqHgr6@mosquitto \
         --devices="10.0.10.110 10.0.10.160"
```

Two TP-Link plugs sit on the LAN; both are polled every 3 s. There is no
Home Assistant autodiscovery, no config file, no LWT, and the topic layout
diverges from the other bridges in the same stack:

```
hs100/maintenance/_bridge/online          -> bool (retained)
hs100/maintenance/<opaque-deviceId>/online -> bool (retained)
hs100/status/<opaque-deviceId>            -> JSON {val,power,voltage,current,energy}
hs100/set/<opaque-deviceId>               <- bool | {val:bool}
```

The sibling bridges (`velux-mqtt-gw`, `hue-to-mqtt-gw`, `miele-to-mqtt-gw`)
share a common shape: Go 1.25+, JSON config with `${ENV}` substitution,
`philipparndt/mqtt-gateway` for MQTT plumbing (including LWT), pprof on
`:6060`, GoReleaser + distroless. We adopt that shape and reshape the topic
layout to match, using user-supplied device names rather than the opaque
TP-Link `deviceId`.

## Goals / Non-Goals

**Goals**
- Feature parity with the current bridge for the two deployed plugs.
- HS100 (no emeter) and HS110 (with emeter) supported by the same binary
  with **runtime feature detection** — no per-device flag in the config.
- Style-consistent with `hue-to-mqtt-gw` and `velux-mqtt-gw`: package
  layout, config format, logging, MQTT plumbing, build/release.
- Reasonable resilience: reconnect on TCP error, availability tracking,
  bridge LWT.

**Non-Goals**
- UDP-broadcast autodiscovery of TP-Link devices. The deployment uses a
  fixed IP list; the Docker service does not run with `--net=host`.
- Historical topic compatibility with `dersimn/hs100tomqtt`. Two plugs, one
  operator, migrate once (HA auto-discovery makes the migration a
  one-restart affair, see §8).
- Any device beyond single-outlet TP-Link Smart Plugs (bulbs, dimmers,
  light strips). Explicitly out of scope: **multi-outlet strips** (HS107,
  HS300 — sysinfo carries a `children[]` array and commands must wrap
  their payload in `context.child_ids`, adding a whole per-outlet layer
  we don't need); **Kasa/Tapo devices using KLAP or AES auth** (newer
  TP-Link line, not compatible with the classic XOR scheme).

## Decisions

### 1. Topic layout — sibling style, device names

```
{prefix}/bridge/state          online|offline (LWT)
{prefix}/{name}/available      online|offline
{prefix}/{name}/state          JSON payload (retained)
{prefix}/{name}/set            command (subscribed)
{prefix}/{name}/get            triggers republish of /state
```

`{prefix}` from `config.mqtt.topic` (default `hs100`). `{name}` is the
config alias for each device — human-readable, stable, and distinct from
the opaque TP-Link `deviceId`. Rationale: hue2mqtt uses `names` and
subscribes to `topic/#`, distinguishing commands from state by suffix; we
adopt the same pattern.

**Payload shape:**
```json
// HS100 (no emeter)
{"on": true}

// HS110 (with emeter)
{"on": true, "power_w": 42.1, "voltage_v": 230.5,
 "current_a": 0.183, "energy_kwh": 12.4}
```

Numeric units are suffixed so the JSON is self-documenting. `on` is used
instead of `val` to align with the sibling bridges' vocabulary.

**Command payload accepted on `/set`:**
- `true` / `false` (raw boolean JSON)
- `"ON"` / `"OFF"` / `"on"` / `"off"` (string)
- `{"on": true}` / `{"on": false}` (object form matching state)
- Any other payload is logged at `error` and dropped.

### 2. TP-Link protocol — hand-rolled, no third-party dep

The TP-Link Smart-Plug protocol has been publicly reverse-engineered and
is well documented (softscheck 2016; leelavg / plasticrake source). The
whole surface fits in ~150 LOC:

```
Wire format (TCP :9999)
┌─────────────┬────────────────────────────────────────┐
│ length (4B) │ payload (XOR-obfuscated bytes)         │
│  big-endian │                                        │
└─────────────┴────────────────────────────────────────┘

XOR scheme (rolling):
  key := 0xAB        ; = 171
  for each byte b in plaintext:
    c := b XOR key
    emit c
    key := c         ; next byte's key = ciphertext byte

Decrypt is the mirror: key = ciphertext byte, plaintext = c XOR key.

Commands (JSON), single-frame batch on the same connection:
  {"system":{"get_sysinfo":{}}, "emeter":{"get_realtime":{}}}
  {"system":{"set_relay_state":{"state":0|1}}}
```

Two protocol details worth calling out (both cross-checked against
`plasticrake/tplink-smarthome-api`, the same lineage as the current
NodeJS bridge):

- **TCP segmentation**: the plug can (and does) split its response
  across multiple TCP segments. The client MUST accumulate bytes until
  `len(buffer) - 4 >= expectedLen` (the length prefix), *only then*
  decrypt the concatenated ciphertext. Decrypting per-segment corrupts
  the rolling XOR state and produces garbage. The `net.Conn` read loop
  in `client.go` is written accordingly.
- **Batch requests**: multiple modules can be queried in one framed
  request. On an HS110 poll we send
  `{"system":{"get_sysinfo":{}}, "emeter":{"get_realtime":{}}}` in a
  single frame — one TCP connect per poll instead of two. On an HS100
  we omit the `emeter` block. This is what the NodeJS bridge does and
  what real plugs are optimised for.

**Package `tplink/`:**
- `protocol.go`: `Encode(payload []byte) []byte`, `Decode(frame []byte) ([]byte, error)`;
  no I/O.
- `client.go`: `Client` with `Host string`, `Timeout time.Duration`; methods
  `Poll(ctx, wantEmeter bool) (SysInfo, *EmeterRealtime, error)` (single
  round-trip that batches sysinfo + optional emeter) and
  `SetRelay(ctx, on bool) error`. Each call opens a fresh TCP connection,
  sends one framed request, reads one framed response, closes — matching
  how the plug actually behaves (it does not hold state across
  connections, and short-lived sockets are what the reference NodeJS lib
  does too). The read loop accumulates bytes until
  `len(buf) - 4 >= expectedLen` before decrypting, to survive TCP
  segmentation.
- `types.go`: `SysInfo` (fields `Model`, `Alias`, `DeviceID`, `SwVer`,
  `HwVer`, `Feature`, `RelayState *int` — pointer so missing values are
  distinguishable from `0`), `EmeterRealtime`, and a `HasEmeterFeature`
  helper.
- **Defensive parsing**: any JSON error, missing `err_code`, non-zero
  `err_code`, or absent `system.get_sysinfo.relay_state` is treated as
  a poll failure (bubbles up to the manager's backoff loop). No silent
  defaulting to `false`.

Rationale for no library: `github.com/jaedle/golang-tplink-hs100` and
`github.com/janniedejong/hs100/hs100` are one-maintainer projects with
minimal activity; the protocol is 15 years old and stable; owning it
avoids the fate of the current NodeJS lib. Consistent with the "own your
driver" pattern in `velux/klf200` and `hue/hue`. The upstream NodeJS
`plasticrake/tplink-smarthome-api` (which the current bridge uses) is
the primary cross-reference for behaviour — its `tcp-socket.ts`
buffering loop and `plug/index.ts` module-namespace layout drive the
two protocol details called out above.

### 3. HS100 vs HS110 — runtime detection via `sysinfo.feature`

`get_sysinfo` returns a `feature` field:

| Model | `feature` value |
|-------|-----------------|
| HS100 | `"TIM"`         |
| HS110 | `"TIM:ENE"`     |

The device-manager caches this after the first successful `get_sysinfo`
per device:

```go
type Device struct {
    Cfg       config.Device
    Client    *tplink.Client
    HasEmeter bool   // set after first sysinfo
}
```

On each poll cycle the manager fetches `get_sysinfo`. If `HasEmeter` is
true, it additionally fetches `emeter.get_realtime` and merges the values
into the published state. On reconnect (after an error), the flag is not
reset — feature capability is a device property, not a connection
property. If a device is physically replaced (HS110 swapped for HS100 at
the same IP), a bridge restart re-detects.

**Publish shape branches on `HasEmeter`:**

```
       ┌─────────────────────┐
       │  poll tick          │
       └──────────┬──────────┘
                  │
                  ▼
       ┌─────────────────────┐
       │  get_sysinfo        │
       └──────────┬──────────┘
                  │  error? → mark unavailable, backoff, retry
                  ▼
       ┌─────────────────────┐
       │  first success?     │
       │    yes: set         │
       │      HasEmeter =    │
       │      feature∋"ENE"  │
       └──────────┬──────────┘
                  ▼
       ┌─────────────────────┐
       │  HasEmeter?         │
       └──┬───────────────┬──┘
      no  │           yes │
          ▼               ▼
   publish {on}    get_realtime → publish {on,power_w,…}
```

### 4. Device manager — one goroutine per device

Each configured device runs its own goroutine:

```
┌──────────────────────────────────────────────────────────┐
│  manager.Run(ctx)                                        │
│    for each cfg.Devices → go runDevice(ctx, dev)         │
│                                                          │
│  runDevice(ctx, dev):                                    │
│    ticker := time.NewTicker(cfg.PollingInterval)         │
│    publishAvailability(dev, "online" | "offline")        │
│    for {                                                 │
│      select {                                            │
│      case <-ctx.Done(): return                           │
│      case <-ticker.C:                                    │
│        state, err := dev.poll()                          │
│        if err → mark offline, backoff, continue          │
│        publishStateIfChanged(dev, state)                 │
│      }                                                   │
│    }                                                     │
└──────────────────────────────────────────────────────────┘
```

Availability transitions are published only on change, not every tick.
State is published only when the JSON payload differs from the previously
published value (per-device cache) — MQTT retained-message semantics
handle late subscribers.

**Backoff on error**: exponential from 1 s to 60 s, resetting on the next
successful poll. This is not configurable; the device is either reachable
in a few seconds or the operator has bigger problems.

### 5. Configuration

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

- `${ENV}` substitution comes from `mqtt-gateway/config.ReplaceEnvVariables`
  (as used by hue2mqtt and velux).
- Defaults: `topic=hs100`, `retain=true`, `qos=1`, `polling-interval-seconds=3`,
  `loglevel=info`.
- Validation: `mqtt.url` required; at least one device with non-empty
  `host` and `name`; names must be unique; names must be MQTT-safe (no
  `/`, `#`, `+`) — enforced in the loader.

### 6. Home Assistant auto-discovery

Each configured plug produces one HA **device** (grouping all its
entities under a single card), with entities driven by runtime
detection:

- Always: one `switch` entity for the relay.
- HS110 only: four `sensor` entities — `power_w`, `voltage_v`,
  `current_a`, `energy_kwh`.

Discovery topics follow the standard HA layout with `hs100_` as the
object-id prefix so entities never collide with other bridges:

```
homeassistant/switch/hs100_{name}/config
homeassistant/sensor/hs100_{name}_power/config     (HS110 only)
homeassistant/sensor/hs100_{name}_voltage/config   (HS110 only)
homeassistant/sensor/hs100_{name}_current/config   (HS110 only)
homeassistant/sensor/hs100_{name}_energy/config    (HS110 only)
```

Switch discovery payload (retained):

```json
{
  "name": "Relay",
  "unique_id": "hs100_{name}_relay",
  "state_topic": "{prefix}/{name}/state",
  "value_template": "{{ 'ON' if value_json.on else 'OFF' }}",
  "command_topic": "{prefix}/{name}/set",
  "payload_on": "ON",
  "payload_off": "OFF",
  "availability_topic": "{prefix}/{name}/available",
  "payload_available": "online",
  "payload_not_available": "offline",
  "device": {
    "identifiers": ["hs100_{name}"],
    "name": "{name}",
    "manufacturer": "TP-Link",
    "model": "{sysinfo.model}",
    "sw_version": "{sysinfo.sw_ver}"
  }
}
```

Sensor discovery (one example — the others follow the same shape with
their own `value_template`, `unit_of_measurement`, `device_class`):

```json
{
  "name": "Power",
  "unique_id": "hs100_{name}_power",
  "state_topic": "{prefix}/{name}/state",
  "value_template": "{{ value_json.power_w }}",
  "unit_of_measurement": "W",
  "device_class": "power",
  "state_class": "measurement",
  "availability_topic": "{prefix}/{name}/available",
  "device": { … same identifiers as the switch above … }
}
```

**Timing**: discovery for a device is published on the first successful
poll (once `sysinfo.model` / `sw_ver` are known and `HasEmeter` is
resolved), so HA never sees a switch with unknown model or a phantom
sensor for a plug that turns out to be HS100.

**Cleanup**: on graceful shutdown (`SIGINT`/`SIGTERM`), the bridge
publishes an empty retained payload to every discovery topic it
previously published, causing HA to remove the entities. This matches
the velux bridge's approach.

**Sensor unit table:**

| Metric   | Unit | `device_class` | `state_class` |
|----------|------|----------------|---------------|
| power_w  | W    | power          | measurement   |
| voltage_v| V    | voltage        | measurement   |
| current_a| A    | current        | measurement   |
| energy_kwh| kWh | energy         | total_increasing |

### 7. Docker-Compose integration

Replaces the current `hs2mqtt` block:

```yaml
hs2mqtt:
  image: ghcr.io/mqtt-home/hs100-to-mqtt-gw:latest
  container_name: hs2mqtt
  volumes:
    - ./config/hs2mqtt/config.json:/var/lib/hs2mqtt/config.json:ro
  environment:
    - MQTT_PASSWORD=UR23x2jqHgr6
  networks: [mosquitto]
  restart: always
```

The image `CMD` defaults to `/hs2mqtt /var/lib/hs2mqtt/config.json`.
`--net=host` is intentionally not used; static IP list only.

### 8. Package layout

Mirroring `hue-to-mqtt-gw`:

```
app/
├── main.go                     bootstrap, signal handling, wire deps
├── go.mod                      module github.com/mqtt-home/hs2mqtt
├── Makefile
├── Dockerfile.goreleaser
├── Dockerfile.goreleaser-arm
├── .goreleaser.yml
├── .golangci.yml
├── config/
│   ├── config.go               Config, LoadConfig, ApplyDefaults, validate
│   └── config_test.go
├── tplink/
│   ├── protocol.go             Encode/Decode + framing
│   ├── protocol_test.go
│   ├── client.go               Client, GetSysInfo, GetEmeter, SetRelay
│   ├── client_test.go          (against a fake TCP server)
│   └── types.go                SysInfo, Emeter, feature detection helper
├── bridge/
│   ├── manager.go              per-device goroutines, polling loop, cache
│   ├── manager_test.go
│   ├── mqtt.go                 publish/subscribe adapter over mqtt-gateway
│   ├── payload.go              JSON encode/decode of state & set commands
│   └── payload_test.go
├── hadiscovery/
│   ├── discovery.go            Build & publish discovery payloads; track topics; cleanup
│   ├── discovery_test.go
│   └── payloads.go             Switch + sensor payload templates
└── version/
    └── version.go              Version, GitCommit, BuildTime (ldflags)
```

`production/config/config-example.json` at the repo root, matching the
sibling repos.

## Risks / Trade-offs

- **Topic breaking change**. Existing Home Assistant sensors/switches that
  subscribe to `hs100/status/<deviceId>` or publish to `hs100/set/<deviceId>`
  will silently stop working until reconfigured. Mitigation: only two
  plugs in the deployment; document the migration in the README.
- **Hand-rolled protocol vs library**. Owning ~150 LOC of driver means
  owning its bugs. Mitigation: the protocol is stable and well documented;
  a fake-TCP-server unit test round-trips real-world sysinfo payloads
  captured from the two live devices during initial testing.
- **HS100 detection false negatives**. If TP-Link ever ships a firmware
  where `feature` is missing or renamed, HS100 could be misdetected as
  HS110 and produce spurious zero readings. Mitigation: treat missing
  `emeter` block on the first poll as an implicit "no emeter" and clear
  the flag (belt-and-braces); log at `warn`.
- **Per-poll TCP connect overhead**. Every 3 s we open+close a socket to
  each plug. This is what the NodeJS lib does today and what the plugs
  are designed for — but if the polling interval is tightened later, a
  connection cache may become worthwhile. Deferred; not needed at 3 s.

## Migration Plan

1. Deliver the Go binary + Docker image (`ghcr.io/mqtt-home/hs100-to-mqtt-gw`)
   on a versioned tag.
2. Publish `production/config/config-example.json`.
3. On the homeserver:
   - Remove any existing hand-crafted HA yaml entities for the plugs
     (they would otherwise duplicate the new auto-discovered ones).
   - Create `config/hs2mqtt/config.json` from the example.
   - Update `homeserver/docker-compose.yaml` to the new image + volume +
     env (see §7).
   - `docker compose up -d hs2mqtt`.
4. Verify:
   - `{prefix}/bridge/state` → `online`
   - Both devices publish `{prefix}/{name}/available` → `online`
   - `state` payload matches HS100 vs HS110 expectation
   - HA discovers each plug as one device with a switch entity, and
     the HS110 also as four sensor entities.

## Open Questions

<!-- Resolved:
  - Docker image publish target → ghcr.io/mqtt-home/hs100-to-mqtt-gw
  - HA discovery → in scope for this change (§6, ha-discovery capability)
-->
None outstanding.
