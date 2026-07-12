## ADDED Requirements

### Requirement: Frame encoding

The driver SHALL encode outgoing commands with a 4-byte big-endian length
prefix followed by rolling-XOR-obfuscated payload bytes (initial key
`171`; each ciphertext byte becomes the key for the next plaintext byte).

#### Scenario: Length prefix

- **WHEN** a JSON payload of N bytes is encoded
- **THEN** the resulting frame starts with N as a 4-byte big-endian integer

#### Scenario: XOR rolling key

- **WHEN** the plaintext bytes are `p0 p1 p2 …`
- **THEN** the ciphertext bytes are `c0=p0^171, c1=p1^c0, c2=p2^c1, …`

### Requirement: Frame decoding

The driver SHALL decode incoming frames by reading the 4-byte big-endian
length, reading exactly that many payload bytes, and reversing the
rolling-XOR obfuscation to recover the JSON plaintext.

#### Scenario: Round trip

- **WHEN** a payload is encoded and then decoded
- **THEN** the result equals the original bytes

#### Scenario: Length mismatch

- **WHEN** the incoming stream contains fewer payload bytes than the length prefix declares
- **THEN** decoding returns an error and the caller closes the connection

### Requirement: TCP segmentation buffering

The driver SHALL accumulate incoming TCP bytes into a receive buffer
and SHALL only attempt decryption once the buffered length minus 4
(the length-prefix bytes) is greater than or equal to the value
carried by the length prefix. Per-segment decryption is forbidden
because the rolling-XOR state spans the entire ciphertext.

#### Scenario: Multi-segment response

- **WHEN** a plug's response is delivered as two TCP segments totalling `4 + N` bytes
- **THEN** the client accumulates both segments before decrypting and returns a single valid JSON payload

### Requirement: Batched poll query

The driver SHALL implement `Poll(ctx, wantEmeter bool)` that opens a
fresh TCP connection on port 9999, sends a single framed request that
includes `system.get_sysinfo` and, when `wantEmeter` is true, also
`emeter.get_realtime`, reads and decodes one response frame, and
returns the parsed `SysInfo` and a `*EmeterRealtime` (nil when
`wantEmeter` is false or when the response omits the `emeter` block).

#### Scenario: HS100 poll (sysinfo only)

- **WHEN** `Poll(ctx, false)` is called against a reachable plug
- **THEN** the outgoing frame carries `{"system":{"get_sysinfo":{}}}` and the returned emeter pointer is nil

#### Scenario: HS110 poll (sysinfo + emeter)

- **WHEN** `Poll(ctx, true)` is called against a reachable plug
- **THEN** the outgoing frame carries both `system.get_sysinfo` and `emeter.get_realtime` in one JSON object
- **AND** the response is parsed for both modules in one round trip

#### Scenario: Timeout

- **WHEN** the plug does not respond within the configured timeout
- **THEN** `Poll` returns a non-nil error and the caller can retry

#### Scenario: Missing relay_state

- **WHEN** the response omits `system.get_sysinfo.relay_state`
- **THEN** `Poll` returns an error (relay_state is not silently defaulted to 0)

### Requirement: Emeter normalisation

`EmeterRealtime` values SHALL carry power in watts, voltage in volts,
current in amperes, and total energy in kilowatt-hours, normalising
firmware variants that report either scaled integer fields
(`power_mw`, `voltage_mv`, `current_ma`, `total_wh`) or unscaled
floats (`power`, `voltage`, `current`, `total`).

#### Scenario: Scaled integer firmware

- **WHEN** the plug returns `{"power_mw":41200,"voltage_mv":230500,"current_ma":183,"total_wh":12400}`
- **THEN** the returned value is `{PowerW: 41.2, VoltageV: 230.5, CurrentA: 0.183, EnergyKwh: 12.4}`

#### Scenario: Unscaled float firmware

- **WHEN** the plug returns `{"power":41.2,"voltage":230.5,"current":0.183,"total":12.4}`
- **THEN** the returned value equals the above

### Requirement: Relay command

The driver SHALL implement `SetRelay(ctx, on bool)` that sends the framed
JSON `{"system":{"set_relay_state":{"state":1}}}` for `on=true` or
`state:0` for `on=false`, and returns an error if the response
`err_code` is non-zero.

#### Scenario: Success

- **WHEN** the plug accepts the command and responds with `err_code:0`
- **THEN** `SetRelay` returns `nil`

#### Scenario: Rejected

- **WHEN** the plug responds with a non-zero `err_code`
- **THEN** `SetRelay` returns an error carrying that code and its message

### Requirement: Defensive response parsing

The driver SHALL treat any of the following as a failed response and
return a non-nil error rather than a partially populated value:
malformed JSON, missing top-level module block, missing `err_code`,
non-zero `err_code`, or a `system.get_sysinfo` payload lacking the
`relay_state` field.

#### Scenario: Malformed JSON

- **WHEN** the decrypted response is not valid JSON
- **THEN** the driver returns an error carrying the raw string prefix and the caller triggers backoff

#### Scenario: Non-zero err_code

- **WHEN** the response contains `"err_code": -1, "err_msg": "invalid argument"`
- **THEN** the driver returns an error wrapping the code and message

### Requirement: HS100 vs HS110 feature helper

The driver SHALL expose a `HasEmeterFeature(SysInfo) bool` helper that
returns `true` if and only if the `feature` string of the sysinfo
contains the token `"ENE"`.

#### Scenario: HS100

- **WHEN** `sysinfo.feature == "TIM"`
- **THEN** `HasEmeterFeature` returns `false`

#### Scenario: HS110

- **WHEN** `sysinfo.feature == "TIM:ENE"`
- **THEN** `HasEmeterFeature` returns `true`
