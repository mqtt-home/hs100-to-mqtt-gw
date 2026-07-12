package hadiscovery

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mqtt-home/hs100-to-mqtt-gw/tplink"
)

type publishCall struct {
	topic   string
	payload []byte
	retain  bool
}

type fakePub struct {
	mu    sync.Mutex
	calls []publishCall
}

func (f *fakePub) publish(topic string, payload []byte, retain bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, publishCall{topic: topic, payload: append([]byte(nil), payload...), retain: retain})
}

func (f *fakePub) topics() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.topic)
	}
	sort.Strings(out)
	return out
}

func sampleSys(model string) tplink.SysInfo {
	return tplink.SysInfo{
		Model:   model,
		Alias:   "alias",
		SwVer:   "1.2.3",
		HwVer:   "2.0",
		Feature: "TIM:ENE",
	}
}

func TestPublish_HS100_OneSwitchOnly(t *testing.T) {
	fp := &fakePub{}
	p := NewPublisher("hs100", fp.publish)
	p.Publish("office", sampleSys("HS100"), false)

	got := fp.topics()
	want := []string{"homeassistant/switch/hs100_office/config"}
	if !equal(got, want) {
		t.Fatalf("topics = %v, want %v", got, want)
	}
	if len(p.TrackedTopics()) != 1 {
		t.Errorf("tracked = %d, want 1", len(p.TrackedTopics()))
	}
}

func TestPublish_HS110_SwitchPlusFourSensors(t *testing.T) {
	fp := &fakePub{}
	p := NewPublisher("hs100", fp.publish)
	p.Publish("kitchen", sampleSys("HS110"), true)

	got := fp.topics()
	want := []string{
		"homeassistant/sensor/hs100_kitchen_current/config",
		"homeassistant/sensor/hs100_kitchen_energy/config",
		"homeassistant/sensor/hs100_kitchen_power/config",
		"homeassistant/sensor/hs100_kitchen_voltage/config",
		"homeassistant/switch/hs100_kitchen/config",
	}
	if !equal(got, want) {
		t.Fatalf("topics =\n  %v\nwant\n  %v", got, want)
	}
}

func TestPublish_Idempotent(t *testing.T) {
	fp := &fakePub{}
	p := NewPublisher("hs100", fp.publish)
	p.Publish("kitchen", sampleSys("HS110"), true)
	p.Publish("kitchen", sampleSys("HS110"), true)

	if got, want := len(p.TrackedTopics()), 5; got != want {
		t.Errorf("tracked after re-publish = %d, want %d (idempotent)", got, want)
	}
}

func TestCleanup_ClearsAllPublishedTopics(t *testing.T) {
	fp := &fakePub{}
	p := NewPublisher("hs100", fp.publish)
	p.Publish("hs100dev", sampleSys("HS100"), false) // 1 topic
	p.Publish("hs110dev", sampleSys("HS110"), true)  // 5 topics

	publishedCount := len(fp.calls)
	if publishedCount != 6 {
		t.Fatalf("published %d, want 6", publishedCount)
	}

	p.Cleanup()

	if len(p.TrackedTopics()) != 0 {
		t.Errorf("TrackedTopics after cleanup = %d, want 0", len(p.TrackedTopics()))
	}

	cleanupCalls := fp.calls[publishedCount:]
	if len(cleanupCalls) != 6 {
		t.Fatalf("cleanup emitted %d calls, want 6", len(cleanupCalls))
	}
	for _, c := range cleanupCalls {
		if len(c.payload) != 0 {
			t.Errorf("cleanup payload not empty: %q", c.payload)
		}
		if !c.retain {
			t.Errorf("cleanup payload not retained: %s", c.topic)
		}
	}
}

func TestSwitchConfig_Payload(t *testing.T) {
	topic, payload, err := SwitchConfig("hs100", "office", sampleSys("HS100"))
	if err != nil {
		t.Fatal(err)
	}
	if topic != "homeassistant/switch/hs100_office/config" {
		t.Errorf("topic = %q", topic)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload not JSON: %v (raw: %s)", err, payload)
	}
	// Spot-check key fields
	if got["state_topic"] != "hs100/office/state" {
		t.Errorf("state_topic = %v", got["state_topic"])
	}
	if got["command_topic"] != "hs100/office/set" {
		t.Errorf("command_topic = %v", got["command_topic"])
	}
	if got["availability_topic"] != "hs100/office/available" {
		t.Errorf("availability_topic = %v", got["availability_topic"])
	}
	if got["payload_on"] != "ON" || got["payload_off"] != "OFF" {
		t.Errorf("payload_on/off = %v/%v", got["payload_on"], got["payload_off"])
	}
	tmpl, _ := got["value_template"].(string)
	if !strings.Contains(tmpl, "value_json.on") {
		t.Errorf("value_template = %q", tmpl)
	}
	dev, ok := got["device"].(map[string]interface{})
	if !ok {
		t.Fatal("device block missing")
	}
	if dev["manufacturer"] != "TP-Link" {
		t.Errorf("manufacturer = %v", dev["manufacturer"])
	}
	if dev["model"] != "HS100" {
		t.Errorf("model = %v", dev["model"])
	}
	if dev["sw_version"] != "1.2.3" {
		t.Errorf("sw_version = %v", dev["sw_version"])
	}
	ids, _ := dev["identifiers"].([]interface{})
	if len(ids) != 1 || ids[0] != "hs100_office" {
		t.Errorf("identifiers = %v", ids)
	}
}

func TestSensorConfig_UnitsAndClasses(t *testing.T) {
	cases := []struct {
		m         Metric
		unit      string
		devClass  string
		stateCls  string
		valueKey  string
	}{
		{MetricPower, "W", "power", "measurement", "power_w"},
		{MetricVoltage, "V", "voltage", "measurement", "voltage_v"},
		{MetricCurrent, "A", "current", "measurement", "current_a"},
		{MetricEnergy, "kWh", "energy", "total_increasing", "energy_kwh"},
	}
	for _, c := range cases {
		t.Run(string(c.m), func(t *testing.T) {
			topic, payload, err := SensorConfig("hs100", "dev", sampleSys("HS110"), c.m)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(topic, "/config") {
				t.Errorf("topic = %q", topic)
			}
			var got map[string]interface{}
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatal(err)
			}
			if got["unit_of_measurement"] != c.unit {
				t.Errorf("unit = %v, want %s", got["unit_of_measurement"], c.unit)
			}
			if got["device_class"] != c.devClass {
				t.Errorf("device_class = %v", got["device_class"])
			}
			if got["state_class"] != c.stateCls {
				t.Errorf("state_class = %v", got["state_class"])
			}
			tmpl := got["value_template"].(string)
			if !strings.Contains(tmpl, c.valueKey) {
				t.Errorf("value_template %q missing key %q", tmpl, c.valueKey)
			}
		})
	}
}

func TestObjectID_Namespaced(t *testing.T) {
	got := objectID("office")
	if got != "hs100_office" {
		t.Errorf("objectID = %q, want %q", got, "hs100_office")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
