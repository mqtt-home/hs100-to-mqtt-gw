package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mqtt-home/hs100-to-mqtt-gw/tplink"
)

// StatePayload is the JSON shape published to `{prefix}/{name}/state`.
// The four measurement fields are pointers so they are omitted from the
// marshalled JSON for HS100 devices (which have no emeter).
type StatePayload struct {
	On        bool     `json:"on"`
	PowerW    *float64 `json:"power_w,omitempty"`
	VoltageV  *float64 `json:"voltage_v,omitempty"`
	CurrentA  *float64 `json:"current_a,omitempty"`
	EnergyKwh *float64 `json:"energy_kwh,omitempty"`
}

// MarshalState builds the retained state payload for a device. Emeter fields
// are included iff hasEmeter is true and emeter is non-nil.
func MarshalState(hasEmeter bool, sys tplink.SysInfo, emeter *tplink.EmeterRealtime) ([]byte, error) {
	p := StatePayload{On: sys.RelayOn()}
	if hasEmeter && emeter != nil {
		p.PowerW = &emeter.PowerW
		p.VoltageV = &emeter.VoltageV
		p.CurrentA = &emeter.CurrentA
		p.EnergyKwh = &emeter.EnergyKwh
	}
	return json.Marshal(p)
}

// ParseSetCommand interprets any of the three accepted forms on
// `{prefix}/{name}/set` and returns the requested relay state.
// Accepted forms:
//   - raw JSON boolean: `true` / `false`
//   - ON/OFF string (case-insensitive), with or without JSON quotes: `"ON"`, `ON`, `off`
//   - object: `{"on": true}` / `{"on": false}`
func ParseSetCommand(payload []byte) (bool, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return false, errors.New("empty payload")
	}
	// JSON `null` unmarshals into a bool as a silent zero — reject explicitly
	// so we don't turn plugs off on a stray null payload.
	if string(trimmed) == "null" {
		return false, errors.New("null payload not accepted")
	}

	// Object form takes precedence — try it first.
	if trimmed[0] == '{' {
		var obj struct {
			On *bool `json:"on"`
		}
		if err := json.Unmarshal(trimmed, &obj); err == nil && obj.On != nil {
			return *obj.On, nil
		}
		return false, fmt.Errorf("object payload missing 'on' field: %s", string(trimmed))
	}

	// Raw JSON boolean.
	var b bool
	if err := json.Unmarshal(trimmed, &b); err == nil {
		return b, nil
	}

	// ON/OFF string, possibly quoted.
	s := strings.Trim(string(trimmed), `"`)
	switch strings.ToLower(s) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}

	return false, fmt.Errorf("unrecognised set payload: %s", string(trimmed))
}
