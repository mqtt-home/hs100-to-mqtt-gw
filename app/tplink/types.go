// Package tplink implements a small driver for classic TP-Link Smart Plugs
// (HS100 / HS110) over the reverse-engineered TCP :9999 XOR-obfuscated
// protocol. It targets single-outlet devices and deliberately excludes
// multi-outlet strips (HS107/HS300) and the newer KLAP/AES auth line
// (Kasa/Tapo).
package tplink

import "strings"

// SysInfo is the parsed shape of `system.get_sysinfo` for a single-outlet
// smart plug. RelayState is a pointer so that a missing value in the payload
// is distinguishable from an explicit 0 — the driver treats a missing value
// as a failed response.
type SysInfo struct {
	Model      string `json:"model"`
	Alias      string `json:"alias"`
	DeviceID   string `json:"deviceId"`
	SwVer      string `json:"sw_ver"`
	HwVer      string `json:"hw_ver"`
	Feature    string `json:"feature"`
	RelayState *int   `json:"relay_state"`
	MAC        string `json:"mac,omitempty"`
}

// RelayOn returns true iff RelayState is present and non-zero.
func (s SysInfo) RelayOn() bool {
	return s.RelayState != nil && *s.RelayState != 0
}

// HasEmeterFeature reports whether the sysinfo's `feature` field advertises
// the emeter capability. Used to distinguish HS110 (has emeter) from HS100
// (does not). Matches the check used by the upstream NodeJS reference lib.
func HasEmeterFeature(s SysInfo) bool {
	return strings.Contains(s.Feature, "ENE")
}

// EmeterRealtime is a normalised view of `emeter.get_realtime`, expressed in
// SI units regardless of which firmware variant the device runs:
//   - RealtimeV1 (older): fractional floats {current, power, voltage, total}
//   - RealtimeV2 (newer): scaled ints {current_ma, power_mw, voltage_mv, total_wh}
//
// See UnmarshalJSON for the normalisation rules.
type EmeterRealtime struct {
	PowerW    float64
	VoltageV  float64
	CurrentA  float64
	EnergyKwh float64
}

// emeterRaw mirrors both firmware variants; UnmarshalJSON picks whichever is
// present and normalises to SI units on EmeterRealtime.
type emeterRaw struct {
	// V1 (unscaled floats)
	Current *float64 `json:"current"`
	Power   *float64 `json:"power"`
	Voltage *float64 `json:"voltage"`
	Total   *float64 `json:"total"`
	// V2 (scaled integers: milli-*)
	CurrentMA *float64 `json:"current_ma"`
	PowerMW   *float64 `json:"power_mw"`
	VoltageMV *float64 `json:"voltage_mv"`
	TotalWh   *float64 `json:"total_wh"`
}

// pick returns v1 if non-nil, otherwise scaled/divisor of v2 if non-nil,
// otherwise 0. `divisor` converts the milli-scaled integer to its SI unit.
func pick(v1, v2 *float64, divisor float64) float64 {
	switch {
	case v1 != nil:
		return *v1
	case v2 != nil:
		return *v2 / divisor
	}
	return 0
}

// fromRaw converts the raw JSON view into a normalised EmeterRealtime.
func (e *EmeterRealtime) fromRaw(r emeterRaw) {
	e.PowerW = pick(r.Power, r.PowerMW, 1000)
	e.VoltageV = pick(r.Voltage, r.VoltageMV, 1000)
	e.CurrentA = pick(r.Current, r.CurrentMA, 1000)
	// Energy is Wh -> kWh, so divisor is 1000. RealtimeV1 already reports
	// kWh via the `total` field, so the ratio is preserved.
	e.EnergyKwh = pick(r.Total, r.TotalWh, 1000)
}
