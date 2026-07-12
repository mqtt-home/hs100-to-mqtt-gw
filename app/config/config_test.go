package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

func TestLoadConfig_DefaultsApplied(t *testing.T) {
	p := writeTemp(t, `{
		"mqtt": {"url": "tcp://broker:1883"},
		"devices": [{"host": "10.0.0.1", "name": "one"}]
	}`)

	c, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if c.MQTT.QoS != 1 {
		t.Errorf("QoS default = %d, want 1", c.MQTT.QoS)
	}
	if c.MQTT.Topic != "hs100" {
		t.Errorf("Topic default = %q, want %q", c.MQTT.Topic, "hs100")
	}
	if c.MQTT.Retain != true {
		t.Errorf("Retain default = %v, want true", c.MQTT.Retain)
	}
	if c.PollingIntervalSeconds != 3 {
		t.Errorf("PollingIntervalSeconds default = %d, want 3", c.PollingIntervalSeconds)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel default = %q, want %q", c.LogLevel, "info")
	}
}

func TestLoadConfig_ExplicitFalseRetained(t *testing.T) {
	p := writeTemp(t, `{
		"mqtt": {"url": "tcp://broker:1883", "retain": false},
		"devices": [{"host": "10.0.0.1", "name": "one"}]
	}`)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.MQTT.Retain != false {
		t.Errorf("Retain = %v, want false", c.MQTT.Retain)
	}
}

func TestLoadConfig_EnvSubstitution(t *testing.T) {
	t.Setenv("MQTT_PASSWORD_TEST", "secret42")
	p := writeTemp(t, `{
		"mqtt": {"url": "tcp://broker:1883", "password": "${MQTT_PASSWORD_TEST}"},
		"devices": [{"host": "10.0.0.1", "name": "one"}]
	}`)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.MQTT.Password != "secret42" {
		t.Errorf("password = %q, want %q", c.MQTT.Password, "secret42")
	}
}

func TestLoadConfig_MissingURL(t *testing.T) {
	p := writeTemp(t, `{
		"mqtt": {"url": ""},
		"devices": [{"host": "10.0.0.1", "name": "one"}]
	}`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "mqtt.url") {
		t.Fatalf("want error mentioning mqtt.url, got %v", err)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := LoadConfig("/does/not/exist/config.json")
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestLoadConfig_EmptyPath(t *testing.T) {
	_, err := LoadConfig("")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-path error, got %v", err)
	}
}

func TestLoadConfig_EmptyDevices(t *testing.T) {
	p := writeTemp(t, `{
		"mqtt": {"url": "tcp://broker:1883"},
		"devices": []
	}`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "devices") {
		t.Fatalf("want devices error, got %v", err)
	}
}

func TestLoadConfig_DuplicateNames(t *testing.T) {
	p := writeTemp(t, `{
		"mqtt": {"url": "tcp://broker:1883"},
		"devices": [
			{"host": "10.0.0.1", "name": "dup"},
			{"host": "10.0.0.2", "name": "dup"}
		]
	}`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestLoadConfig_WildcardInName(t *testing.T) {
	cases := []string{"a/b", "a#", "a+"}
	for _, name := range cases {
		body := `{
			"mqtt": {"url": "tcp://broker:1883"},
			"devices": [{"host": "10.0.0.1", "name": "` + name + `"}]
		}`
		p := writeTemp(t, body)
		_, err := LoadConfig(p)
		if err == nil {
			t.Errorf("name %q: want error, got nil", name)
		}
	}
}

func TestLoadConfig_EmptyHost(t *testing.T) {
	p := writeTemp(t, `{
		"mqtt": {"url": "tcp://broker:1883"},
		"devices": [{"host": "", "name": "one"}]
	}`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("want host error, got %v", err)
	}
}

func TestLoadConfig_EmptyName(t *testing.T) {
	p := writeTemp(t, `{
		"mqtt": {"url": "tcp://broker:1883"},
		"devices": [{"host": "10.0.0.1", "name": ""}]
	}`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("want name error, got %v", err)
	}
}

func TestMQTTConfig_ToGatewayConfig(t *testing.T) {
	m := MQTTConfig{
		URL:      "tcp://x:1883",
		Username: "u",
		Password: "p",
		Retain:   true,
		QoS:      2,
		Topic:    "t",
	}
	g := m.ToGatewayConfig()
	if g.URL != m.URL || g.Username != m.Username || g.Password != m.Password ||
		g.Retain != m.Retain || g.QoS != m.QoS || g.Topic != m.Topic {
		t.Errorf("gateway config mismatch: %+v vs %+v", g, m)
	}
}

func TestGet_ReturnsLastLoaded(t *testing.T) {
	p := writeTemp(t, `{
		"mqtt": {"url": "tcp://broker:1883"},
		"devices": [{"host": "10.0.0.1", "name": "one"}]
	}`)
	_, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := Get()
	if got.MQTT.URL != "tcp://broker:1883" {
		t.Errorf("Get().MQTT.URL = %q", got.MQTT.URL)
	}
}
