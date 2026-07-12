## 1. Repository scaffolding

- [x] 1.1 Create `app/` directory with `go.mod` (`module github.com/mqtt-home/hs100-to-mqtt-gw`, `go 1.25`)
- [x] 1.2 Add `github.com/philipparndt/go-logger` and `github.com/philipparndt/mqtt-gateway` dependencies
- [x] 1.3 Create `app/version/version.go` with `Version`, `GitCommit`, `BuildTime` package variables set via ldflags
- [x] 1.4 Copy `.golangci.yml` style (short) from hue2mqtt as baseline
- [x] 1.5 Add repo `.gitignore` (Go build artefacts, `production/config/config.json`)

## 2. Configuration

- [x] 2.1 Implement `config/config.go` with `Config`, `MQTTConfig`, `Device` structs
- [x] 2.2 Implement `LoadConfig(path)` — read file, `ReplaceEnvVariables`, unmarshal, apply defaults, validate
- [x] 2.3 Defaults: `MQTT.Retain=true`, `MQTT.QoS=1`, `MQTT.Topic="hs100"`, `PollingIntervalSeconds=3`, `LogLevel="info"`
- [x] 2.4 Validation: `mqtt.url` required; at least one device; each device requires non-empty `host` and `name`; names unique; names must not contain `/`, `#`, `+`
- [x] 2.5 Implement `MQTTConfig.ToGatewayConfig()` returning `gwconfig.MQTTConfig`
- [x] 2.6 Add `config/config_test.go` covering: defaults applied, env substitution, missing url rejected, empty devices rejected, duplicate names rejected, invalid characters in name rejected
- [x] 2.7 Add `production/config/config-example.json` with a two-device sample

## 3. TP-Link driver — protocol

- [x] 3.1 Implement `tplink/protocol.go`: `Encode(plaintext []byte) []byte` (prepends 4-byte BE length, rolling-XOR key=171)
- [x] 3.2 Implement `Decode(frame []byte) ([]byte, error)` — validates length prefix, reverses rolling-XOR
- [x] 3.3 Add `tplink/protocol_test.go` — round-trip encode/decode; decode known ciphertext captured from a live plug matches expected JSON

## 4. TP-Link driver — client

- [x] 4.1 Implement `tplink/types.go`: `SysInfo` (fields `Model`, `Alias`, `DeviceID`, `SwVer`, `HwVer`, `Feature`, `RelayState *int` — pointer so a missing value is distinguishable from `0`), `EmeterRealtime` (`PowerW`, `VoltageV`, `CurrentA`, `EnergyKwh` float64) with a normaliser that accepts both scaled-integer and unscaled-float firmware variants (mW→W, mV→V, mA→A, Wh→kWh; ×1000)
- [x] 4.2 Implement `HasEmeterFeature(SysInfo) bool` — returns true iff `Feature` contains `"ENE"`
- [x] 4.3 Implement `tplink/client.go`: `Client{Host, Port(default 9999), Timeout(default 5s)}`
- [x] 4.4 Implement TCP-segmentation-safe read loop: accumulate bytes until `len(buf)-4 >= expectedLen` (per length prefix), only then decrypt the concatenated ciphertext
- [x] 4.5 Implement `Client.Poll(ctx, wantEmeter bool) (SysInfo, *EmeterRealtime, error)` — TCP dial, single framed request containing `system.get_sysinfo` and (if `wantEmeter`) also `emeter.get_realtime`, parse response for both modules; error on any of: malformed JSON, missing module block, `err_code != 0`, missing `sysinfo.relay_state`
- [x] 4.6 Implement `Client.SetRelay(ctx, on bool) error` — TCP dial, send framed `{"system":{"set_relay_state":{"state":0|1}}}`, check `err_code==0`
- [x] 4.7 Add `tplink/client_test.go` — spin a fake TCP server that speaks the protocol; assert: HS100 poll (sysinfo only, emeter nil), HS110 poll (sysinfo + emeter merged), multi-segment response reassembled correctly, malformed JSON → error, missing relay_state → error, non-zero err_code → error, SetRelay round-trip

## 5. MQTT bridge

- [x] 5.1 Implement `bridge/mqtt.go`: thin wrapper over `mqtt-gateway` for `PublishAbsolute` and `Subscribe`
- [x] 5.2 Implement `bridge/payload.go`: `StatePayload` (fields `On bool`, optional `PowerW`, `VoltageV`, `CurrentA`, `EnergyKwh`), `MarshalState(HasEmeter, sys, emeter) []byte`
- [x] 5.3 Implement `ParseSetCommand(payload []byte) (on bool, err error)` — accepts `true`/`false`, `"ON"`/`"OFF"`, `{"on":bool}`
- [x] 5.4 Add `bridge/payload_test.go` covering all command payload variants + all state payload shapes

## 6. Device manager

- [x] 6.1 Implement `bridge/manager.go`: `Manager` holding `cfg`, `devices []*Device`, MQTT wrapper
- [x] 6.2 `Device` struct: `Cfg config.Device`, `Client *tplink.Client`, `HasEmeter bool`, `lastStateJSON []byte`, `available bool`
- [x] 6.3 `Manager.Run(ctx)` — spawns one goroutine per device via `runDevice(ctx, d)`
- [x] 6.4 `runDevice(ctx, d)` — ticker at `cfg.PollingIntervalSeconds`; on tick: `Poll(ctx, d.HasEmeter)`; on first success set `HasEmeter` from `sysinfo.Feature`; if this was the initial detection *and* HasEmeter is now true, immediately re-poll once to obtain emeter values (avoids a stale empty state); publish state if the JSON payload changed; publish availability transitions
- [x] 6.5 Backoff on error — exponential 1s→2s→4s→…→60s, resets on next successful poll; publish availability `offline` on first failure, `online` on next success
- [x] 6.6 Subscribe `{prefix}/+/set` and `{prefix}/+/get` — resolve name segment to device, on `/set` call `SetRelay` then immediately re-poll and publish, on `/get` re-publish cached state
- [x] 6.7 Add `bridge/manager_test.go` covering: HS100 detection (no emeter block), HS110 detection (emeter block present), state deduplication, availability transitions on error, set command round-trip

## 7. Home Assistant discovery

- [x] 7.1 Implement `hadiscovery/payloads.go`: builders `SwitchConfig(dev, sys)` and `SensorConfig(dev, sys, metric)` where `metric ∈ {power,voltage,current,energy}`; each returns the JSON bytes to publish
- [x] 7.2 Shared device block builder: `{identifiers:["hs100_"+name], name, manufacturer:"TP-Link", model:sys.Model, sw_version:sys.SwVer}`
- [x] 7.3 Value templates: switch → `{{ 'ON' if value_json.on else 'OFF' }}`; sensor → `{{ value_json.<field> }}` matching the state payload
- [x] 7.4 Sensor units and classes per the table in `design.md` §6
- [x] 7.5 Implement `hadiscovery/discovery.go`: `Publisher` with `Publish(dev, sys, hasEmeter)` publishing the switch config and, if HS110, the four sensor configs; SHALL be idempotent (safe to call again on re-detect)
- [x] 7.6 Track every discovery topic published in a `map[string]struct{}` so cleanup knows what to clear
- [x] 7.7 `Publisher.Cleanup()` iterates the tracked topics and publishes an empty retained payload to each; called from graceful shutdown before MQTT disconnect
- [x] 7.8 Wire into `bridge/manager.go`: on the first successful poll per device call `discovery.Publish`; do NOT publish before `sysinfo.Model` and `HasEmeter` are known
- [x] 7.9 Add `hadiscovery/discovery_test.go`: HS100 publishes exactly one topic; HS110 publishes five; cleanup clears exactly the tracked set; re-publish on re-detect does not duplicate topics
- [x] 7.10 Object-id namespacing: object-ids must be prefixed `hs100_` so they never collide with other bridges' entities

## 8. Application runtime

- [x] 8.1 Implement `app/main.go` mirroring `hue-to-mqtt-gw/app/main.go` — log init, parse args (one arg: config path), load config, set log level, start pprof `:6060`, `mqtt.Start(cfg.MQTT.ToGatewayConfig(), "hs2mqtt")`, publish `bridge/state` = `online` (LWT sets `offline`), construct `hadiscovery.Publisher`, start Manager (wired to publisher), signal-wait, graceful shutdown that calls `publisher.Cleanup()` before exit
- [x] 8.2 Confirm `mqtt-gateway` LWT covers `bridge/state` (as it does for hue2mqtt); if not, publish explicitly and set LWT via gateway config
- [x] 8.3 Log at `info` on startup: version, config file, device count; log at `debug` per publish

## 9. Build & deploy

- [x] 9.1 Add `Makefile` mirroring hue2mqtt targets: `build`, `test`, `lint`, `run`, `docker`
- [x] 9.2 Add `Dockerfile.goreleaser` (distroless base, copies pre-built binary) and `Dockerfile.goreleaser-arm`
- [x] 9.3 Add `.goreleaser.yml` producing `linux/amd64` and `linux/arm/v7` binaries + images, matching the hue2mqtt config
- [x] 9.4 Configure goreleaser Docker registry to `ghcr.io/mqtt-home/hs100-to-mqtt-gw` with tags `{{ .Tag }}`, `latest`, and `{{ .Major }}.{{ .Minor }}`
- [x] 9.5 Wire ldflags to set `version.Version`, `version.GitCommit`, `version.BuildTime`

## 10. Deployment integration

- [x] 10.1 Draft the replacement `hs2mqtt` service block for `homeserver/docker-compose.yaml` using `ghcr.io/mqtt-home/hs100-to-mqtt-gw:latest`
- [x] 10.2 Draft the `config/hs2mqtt/config.json` matching the two current plug IPs; names TBD by the operator
- [x] 10.3 Draft the "remove old hand-crafted HA yaml entities" snippet so the auto-discovered ones are not duplicated

## 11. Documentation

- [x] 11.1 Write `README.md` — what/why, config schema, topic layout, HS100 vs HS110 payload difference, HA discovery behaviour, Docker run example, build-from-source
- [x] 11.2 Include a table of accepted `/set` payload forms and the resulting behaviour
- [x] 11.3 Reference the sibling bridges as design source-of-truth
