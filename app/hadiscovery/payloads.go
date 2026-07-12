package hadiscovery

import (
	"encoding/json"
	"fmt"

	"github.com/mqtt-home/hs100-to-mqtt-gw/tplink"
)

// deviceBlock is the shared HA `device` block that groups the switch and
// (for HS110) the four sensor entities under one device card.
type deviceBlock struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	SwVersion    string   `json:"sw_version,omitempty"`
}

func buildDeviceBlock(deviceName string, sys tplink.SysInfo) deviceBlock {
	return deviceBlock{
		Identifiers:  []string{objectID(deviceName)},
		Name:         deviceName,
		Manufacturer: "TP-Link",
		Model:        sys.Model,
		SwVersion:    sys.SwVer,
	}
}

// objectID is the HA object-id / identifier for a given device name, namespaced
// with the `hs100_` prefix so entities never collide with sibling bridges.
func objectID(deviceName string) string {
	return "hs100_" + deviceName
}

// switchConfig is the Home Assistant discovery payload for a plug's relay.
type switchConfig struct {
	Name                string      `json:"name"`
	UniqueID            string      `json:"unique_id"`
	StateTopic          string      `json:"state_topic"`
	ValueTemplate       string      `json:"value_template"`
	CommandTopic        string      `json:"command_topic"`
	PayloadOn           string      `json:"payload_on"`
	PayloadOff          string      `json:"payload_off"`
	AvailabilityTopic   string      `json:"availability_topic"`
	PayloadAvailable    string      `json:"payload_available"`
	PayloadNotAvailable string      `json:"payload_not_available"`
	Device              deviceBlock `json:"device"`
}

// SwitchConfig returns the retained-config JSON bytes to publish to
// `homeassistant/switch/hs100_{name}/config` and the discovery topic.
func SwitchConfig(basePrefix, deviceName string, sys tplink.SysInfo) (topic string, payload []byte, err error) {
	oid := objectID(deviceName)
	cfg := switchConfig{
		Name:                "Relay",
		UniqueID:            oid + "_relay",
		StateTopic:          fmt.Sprintf("%s/%s/state", basePrefix, deviceName),
		ValueTemplate:       "{{ 'ON' if value_json.on else 'OFF' }}",
		CommandTopic:        fmt.Sprintf("%s/%s/set", basePrefix, deviceName),
		PayloadOn:           "ON",
		PayloadOff:          "OFF",
		AvailabilityTopic:   fmt.Sprintf("%s/%s/available", basePrefix, deviceName),
		PayloadAvailable:    "online",
		PayloadNotAvailable: "offline",
		Device:              buildDeviceBlock(deviceName, sys),
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("homeassistant/switch/%s/config", oid), b, nil
}

// Metric identifies one of the four HS110 emeter sensor entities.
type Metric string

const (
	MetricPower   Metric = "power"
	MetricVoltage Metric = "voltage"
	MetricCurrent Metric = "current"
	MetricEnergy  Metric = "energy"
)

// AllMetrics is the ordered list of sensors published on HS110 devices.
var AllMetrics = []Metric{MetricPower, MetricVoltage, MetricCurrent, MetricEnergy}

type sensorSpec struct {
	name          string
	unit          string
	deviceClass   string
	stateClass    string
	valueTemplate string
	stateKey      string // for UniqueID suffix
}

// The state_class for energy is total_increasing because the plug's `total`
// value is a lifetime cumulative kWh reading — HA expects total_increasing
// for cumulative counters that can reset (e.g. after firmware reset).
var sensorSpecs = map[Metric]sensorSpec{
	MetricPower: {
		name: "Power", unit: "W", deviceClass: "power", stateClass: "measurement",
		valueTemplate: "{{ value_json.power_w }}", stateKey: "power",
	},
	MetricVoltage: {
		name: "Voltage", unit: "V", deviceClass: "voltage", stateClass: "measurement",
		valueTemplate: "{{ value_json.voltage_v }}", stateKey: "voltage",
	},
	MetricCurrent: {
		name: "Current", unit: "A", deviceClass: "current", stateClass: "measurement",
		valueTemplate: "{{ value_json.current_a }}", stateKey: "current",
	},
	MetricEnergy: {
		name: "Energy", unit: "kWh", deviceClass: "energy", stateClass: "total_increasing",
		valueTemplate: "{{ value_json.energy_kwh }}", stateKey: "energy",
	},
}

type sensorConfig struct {
	Name                string      `json:"name"`
	UniqueID            string      `json:"unique_id"`
	StateTopic          string      `json:"state_topic"`
	ValueTemplate       string      `json:"value_template"`
	UnitOfMeasurement   string      `json:"unit_of_measurement"`
	DeviceClass         string      `json:"device_class"`
	StateClass          string      `json:"state_class"`
	AvailabilityTopic   string      `json:"availability_topic"`
	PayloadAvailable    string      `json:"payload_available"`
	PayloadNotAvailable string      `json:"payload_not_available"`
	Device              deviceBlock `json:"device"`
}

// SensorConfig returns the retained-config JSON bytes and topic for one of the
// four HS110 sensor entities.
func SensorConfig(basePrefix, deviceName string, sys tplink.SysInfo, m Metric) (topic string, payload []byte, err error) {
	spec, ok := sensorSpecs[m]
	if !ok {
		return "", nil, fmt.Errorf("unknown metric: %s", m)
	}
	oid := objectID(deviceName)
	cfg := sensorConfig{
		Name:                spec.name,
		UniqueID:            oid + "_" + spec.stateKey,
		StateTopic:          fmt.Sprintf("%s/%s/state", basePrefix, deviceName),
		ValueTemplate:       spec.valueTemplate,
		UnitOfMeasurement:   spec.unit,
		DeviceClass:         spec.deviceClass,
		StateClass:          spec.stateClass,
		AvailabilityTopic:   fmt.Sprintf("%s/%s/available", basePrefix, deviceName),
		PayloadAvailable:    "online",
		PayloadNotAvailable: "offline",
		Device:              buildDeviceBlock(deviceName, sys),
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("homeassistant/sensor/%s_%s/config", oid, spec.stateKey), b, nil
}
