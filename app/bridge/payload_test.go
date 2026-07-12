package bridge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mqtt-home/hs100-to-mqtt-gw/tplink"
)

func intPtr(i int) *int { return &i }

func TestMarshalState_HS100(t *testing.T) {
	sys := tplink.SysInfo{RelayState: intPtr(1)}
	b, err := MarshalState(false, sys, nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Must contain "on":true and no measurement fields.
	s := string(b)
	if !strings.Contains(s, `"on":true`) {
		t.Errorf("payload missing on:true: %s", s)
	}
	for _, k := range []string{"power_w", "voltage_v", "current_a", "energy_kwh"} {
		if strings.Contains(s, k) {
			t.Errorf("HS100 payload must not contain %q, got: %s", k, s)
		}
	}
}

func TestMarshalState_HS100_Off(t *testing.T) {
	sys := tplink.SysInfo{RelayState: intPtr(0)}
	b, _ := MarshalState(false, sys, nil)
	if !strings.Contains(string(b), `"on":false`) {
		t.Errorf("payload missing on:false: %s", b)
	}
}

func TestMarshalState_HS110(t *testing.T) {
	sys := tplink.SysInfo{RelayState: intPtr(1)}
	em := &tplink.EmeterRealtime{PowerW: 42.1, VoltageV: 230.5, CurrentA: 0.183, EnergyKwh: 12.4}
	b, err := MarshalState(true, sys, em)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["on"] != true {
		t.Errorf("on = %v, want true", got["on"])
	}
	if got["power_w"] != 42.1 {
		t.Errorf("power_w = %v, want 42.1", got["power_w"])
	}
	if got["voltage_v"] != 230.5 {
		t.Errorf("voltage_v = %v, want 230.5", got["voltage_v"])
	}
	if got["current_a"] != 0.183 {
		t.Errorf("current_a = %v, want 0.183", got["current_a"])
	}
	if got["energy_kwh"] != 12.4 {
		t.Errorf("energy_kwh = %v, want 12.4", got["energy_kwh"])
	}
}

func TestMarshalState_HS110_NilEmeter(t *testing.T) {
	// hasEmeter=true but emeter=nil (e.g. initial detection tick) — must not panic
	// and must publish only the boolean field.
	sys := tplink.SysInfo{RelayState: intPtr(1)}
	b, err := MarshalState(true, sys, nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "power_w") {
		t.Errorf("payload must not contain power_w when emeter is nil, got: %s", b)
	}
}

func TestParseSetCommand(t *testing.T) {
	cases := []struct {
		payload string
		want    bool
		wantErr bool
	}{
		// Boolean form
		{"true", true, false},
		{"false", false, false},
		{" true ", true, false},

		// String form (quoted, JSON)
		{`"ON"`, true, false},
		{`"off"`, false, false},
		{`"OFF"`, false, false},

		// String form (bare)
		{"ON", true, false},
		{"off", false, false},

		// Object form
		{`{"on": true}`, true, false},
		{`{"on": false}`, false, false},
		{`{"on":true}`, true, false},

		// Rejected
		{"", false, true},
		{"garbage", false, true},
		{`{"foo": 1}`, false, true},
		{`{}`, false, true},
		{"null", false, true},
		{"42", false, true},
	}

	for _, c := range cases {
		t.Run(c.payload, func(t *testing.T) {
			got, err := ParseSetCommand([]byte(c.payload))
			if c.wantErr {
				if err == nil {
					t.Errorf("want error, got on=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
