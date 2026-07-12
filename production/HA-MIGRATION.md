# Home Assistant migration — `dersimn/hs100tomqtt` to `hs100-to-mqtt-gw`

The Go rewrite changes the MQTT topic layout and adds MQTT auto-discovery.
This is a breaking change for Home Assistant, but a small one — there are
only two plugs, and HA will pick them up automatically once the new bridge
is running.

## Before

The NodeJS bridge published to topics keyed by TP-Link's opaque `deviceId`
and offered no auto-discovery, so HA typically hand-wired entities against
these topics:

```
hs100/maintenance/_bridge/online              -> bool (retained)
hs100/maintenance/<deviceId>/online           -> bool (retained)
hs100/status/<deviceId>                       -> JSON {val,power,voltage,current,energy}
hs100/set/<deviceId>                          <- bool | {val:bool}
```

## After

The Go bridge publishes to human-readable, name-keyed topics and drives
Home Assistant via MQTT auto-discovery:

```
hs100/bridge/state                            -> online|offline (LWT)
hs100/<name>/available                        -> online|offline (retained)
hs100/<name>/state                            -> JSON {on, [power_w, voltage_v, current_a, energy_kwh]}
hs100/<name>/set                              <- true|false | "ON"|"OFF" | {"on":bool}
hs100/<name>/get                              <- trigger republish of /state
```

Discovery configs are published under `homeassistant/switch/hs100_<name>/config`
and (for HS110) `homeassistant/sensor/hs100_<name>_<metric>/config`. HA
groups all entities of one plug under a single device card.

**Remove any hand-written `switch:` or `sensor:` YAML entries in your HA
config that reference the old `hs100/status/<deviceId>` or
`hs100/set/<deviceId>` topics.** Leaving them in place produces duplicate
entities alongside the auto-discovered ones.

## Cutover order

1. Create `config/hs2mqtt/config.json` on the homeserver (see
   `production/config/config-example.json`). Pick short, stable `name`
   values — they become part of the MQTT topic and the HA entity IDs.
2. Remove the old hand-written `switch:` / `sensor:` YAML entries from
   Home Assistant and reload MQTT / restart HA.
3. Update `homeserver/docker-compose.yaml` with the block from
   `production/docker-compose-hs2mqtt.yaml` and run
   `docker compose up -d hs2mqtt`.
4. Verify: `hs100/bridge/state` is `online`, each `hs100/<name>/available`
   is `online`, and the two plugs appear in HA as devices with a switch
   (both) plus four sensors (HS110 only).

## Rollback

Revert the `hs2mqtt` block in `homeserver/docker-compose.yaml` to the
previous NodeJS image and `docker compose up -d hs2mqtt`. Retained topics
under the new layout will stay on the broker but are harmless — the old
bridge simply doesn't subscribe to them, and HA no longer references them
after step 2.

## Topic mapping — the two plugs

Replace `<deviceId-a>` / `<deviceId-b>` with the opaque IDs the old bridge
was using and `device-a` / `device-b` with the `name` values you chose in
the new config file.

```
Old topic                                     -> New topic
--------------------------------------------------------------------------------
hs100/maintenance/_bridge/online              -> hs100/bridge/state
hs100/maintenance/<deviceId-a>/online         -> hs100/device-a/available
hs100/status/<deviceId-a>                     -> hs100/device-a/state
hs100/set/<deviceId-a>                        -> hs100/device-a/set
(none)                                        -> hs100/device-a/get
hs100/maintenance/<deviceId-b>/online         -> hs100/device-b/available
hs100/status/<deviceId-b>                     -> hs100/device-b/state
hs100/set/<deviceId-b>                        -> hs100/device-b/set
(none)                                        -> hs100/device-b/get
```

Payload shape also changed: the old `{val,power,voltage,current,energy}`
becomes `{on, power_w, voltage_v, current_a, energy_kwh}` on HS110 (units
are in the field names), and `{on}` on HS100.
