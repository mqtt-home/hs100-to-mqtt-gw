package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/philipparndt/go-logger"
	gwconfig "github.com/philipparndt/mqtt-gateway/config"
)

// MQTTConfig holds MQTT broker connection settings.
type MQTTConfig struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Retain   bool   `json:"retain"`
	QoS      byte   `json:"qos"`
	Topic    string `json:"topic,omitempty"`
	ClientID string `json:"client-id,omitempty"`
}

// ToGatewayConfig converts to the shared mqtt-gateway config type.
func (m MQTTConfig) ToGatewayConfig() gwconfig.MQTTConfig {
	return gwconfig.MQTTConfig{
		URL:      m.URL,
		Retain:   m.Retain,
		Topic:    m.Topic,
		QoS:      m.QoS,
		Username: m.Username,
		Password: m.Password,
	}
}

// Device is one configured TP-Link Smart Plug.
type Device struct {
	Host string `json:"host"`
	Name string `json:"name"`
}

// Config is the top-level configuration for the hs100-to-mqtt gateway.
type Config struct {
	MQTT                   MQTTConfig `json:"mqtt"`
	Devices                []Device   `json:"devices"`
	PollingIntervalSeconds int        `json:"polling-interval-seconds,omitempty"`
	LogLevel               string     `json:"loglevel,omitempty"`
}

// ApplyDefaults fills in unset optional fields with their documented defaults.
// Boolean defaults that are true (retain) are pre-seeded before JSON
// unmarshalling in LoadConfig; this function covers numeric/string defaults.
func ApplyDefaults(c *Config) {
	if c.MQTT.QoS == 0 {
		c.MQTT.QoS = 1
	}
	if c.MQTT.Topic == "" {
		c.MQTT.Topic = "hs100"
	}
	if c.PollingIntervalSeconds == 0 {
		c.PollingIntervalSeconds = 3
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

var (
	mu  sync.RWMutex
	cfg Config
)

// LoadConfig reads a JSON config file, substitutes ${ENV} variables,
// applies defaults, validates required fields, and returns the parsed Config.
func LoadConfig(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("config path is empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	data = gwconfig.ReplaceEnvVariables(data)

	// Pre-seed boolean defaults that are true so that an explicit false in the
	// file overrides them correctly after JSON unmarshalling.
	c := Config{
		MQTT: MQTTConfig{Retain: true},
	}

	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	ApplyDefaults(&c)

	if err := validate(&c); err != nil {
		return Config{}, err
	}

	mu.Lock()
	cfg = c
	mu.Unlock()

	logger.Debug("Config loaded", "file", path, "loglevel", c.LogLevel, "devices", len(c.Devices))
	return c, nil
}

// validate returns a descriptive error when any required field is absent or invalid.
func validate(c *Config) error {
	if c.MQTT.URL == "" {
		return errors.New("required field missing: mqtt.url")
	}
	if len(c.Devices) == 0 {
		return errors.New("required field missing: devices (must contain at least one entry)")
	}

	seen := make(map[string]struct{}, len(c.Devices))
	for i, d := range c.Devices {
		if d.Host == "" {
			return fmt.Errorf("devices[%d]: host is empty", i)
		}
		if d.Name == "" {
			return fmt.Errorf("devices[%d]: name is empty", i)
		}
		if strings.ContainsAny(d.Name, "/#+") {
			return fmt.Errorf("devices[%d]: name %q must not contain '/', '#' or '+'", i, d.Name)
		}
		if _, dup := seen[d.Name]; dup {
			return fmt.Errorf("devices[%d]: duplicate name %q", i, d.Name)
		}
		seen[d.Name] = struct{}{}
	}
	return nil
}

// Get returns the currently loaded config (zero value if none loaded yet).
func Get() Config {
	mu.RLock()
	defer mu.RUnlock()
	return cfg
}
