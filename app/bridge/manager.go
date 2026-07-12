package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mqtt-home/hs100-to-mqtt-gw/config"
	"github.com/mqtt-home/hs100-to-mqtt-gw/hadiscovery"
	"github.com/mqtt-home/hs100-to-mqtt-gw/tplink"
	"github.com/philipparndt/go-logger"
)

// Poller is the subset of tplink.Client that Device needs — extracted so tests
// can inject a fake without opening TCP connections.
type Poller interface {
	Poll(ctx context.Context, wantEmeter bool) (tplink.SysInfo, *tplink.EmeterRealtime, error)
	SetRelay(ctx context.Context, on bool) error
}

// Device is one configured plug plus the manager-side runtime state
// (feature-detection flag, cached state payload, availability flag).
type Device struct {
	Cfg    config.Device
	Client Poller

	// hasEmeter is false until the first successful Poll resolves it from
	// sysinfo.feature. It never regresses within a single run.
	hasEmeter        bool
	hasEmeterKnown   bool
	lastStateJSON    []byte
	available        bool
	availableInitial bool // false until availability has been published at least once
}

// Manager owns the polling loop for every configured device and the routing
// of MQTT set/get commands to the right device.
type Manager struct {
	cfg        config.Config
	basePrefix string
	pollEvery  time.Duration

	mu      sync.RWMutex
	devices map[string]*Device // keyed by config name

	mqtt      MQTTClient
	discovery *hadiscovery.Publisher
}

// NewManager builds a Manager. `mqttClient` and `discovery` are wired at
// construction; each configured device becomes a *Device with a fresh
// tplink.Client unless the caller supplied one already (see NewManagerWith
// for test injection).
func NewManager(cfg config.Config, mqttClient MQTTClient, discovery *hadiscovery.Publisher) *Manager {
	m := &Manager{
		cfg:        cfg,
		basePrefix: cfg.MQTT.Topic,
		pollEvery:  time.Duration(cfg.PollingIntervalSeconds) * time.Second,
		devices:    make(map[string]*Device, len(cfg.Devices)),
		mqtt:       mqttClient,
		discovery:  discovery,
	}
	for _, d := range cfg.Devices {
		m.devices[d.Name] = &Device{
			Cfg:    d,
			Client: &tplink.Client{Host: d.Host},
		}
	}
	return m
}

// NewManagerWith is the test-facing constructor that lets a suite inject
// fake Pollers per device name.
func NewManagerWith(cfg config.Config, mqttClient MQTTClient, discovery *hadiscovery.Publisher, pollers map[string]Poller) *Manager {
	m := NewManager(cfg, mqttClient, discovery)
	for name, p := range pollers {
		if d, ok := m.devices[name]; ok {
			d.Client = p
		}
	}
	return m
}

// Run starts one goroutine per configured device and blocks until ctx is
// cancelled. Also installs the shared MQTT subscription for /set and /get
// command topics. Safe to call once per Manager instance.
func (m *Manager) Run(ctx context.Context) {
	m.mqtt.Subscribe(m.basePrefix+"/+/set", m.handleMessage)
	m.mqtt.Subscribe(m.basePrefix+"/+/get", m.handleMessage)

	var wg sync.WaitGroup
	for _, d := range m.devices {
		wg.Add(1)
		go func(d *Device) {
			defer wg.Done()
			m.runDevice(ctx, d)
		}(d)
	}
	wg.Wait()
}

// runDevice is the per-device polling loop. Backoff on error is exponential
// (1s→60s cap) and resets on the next successful poll.
func (m *Manager) runDevice(ctx context.Context, d *Device) {
	const (
		backoffBase = time.Second
		backoffMax  = 60 * time.Second
	)
	backoff := backoffBase

	// Timer instead of Ticker so we can dynamically extend the wait during
	// backoff without a separate coordination mechanism.
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if err := m.pollOnce(ctx, d); err != nil {
			m.publishAvailability(d, false)
			logger.Warn("Poll failed", "device", d.Cfg.Name, "error", err, "backoff", backoff)
			timer.Reset(backoff)
			if backoff < backoffMax {
				backoff *= 2
				if backoff > backoffMax {
					backoff = backoffMax
				}
			}
			continue
		}
		backoff = backoffBase
		m.publishAvailability(d, true)
		timer.Reset(m.pollEvery)
	}
}

// pollOnce executes one poll cycle for a device: handles first-tick
// detection (which may require an immediate re-poll to grab emeter values),
// publishes state on change, and triggers HA discovery on the first success.
func (m *Manager) pollOnce(ctx context.Context, d *Device) error {
	sys, emeter, err := d.Client.Poll(ctx, d.hasEmeter)
	if err != nil {
		return err
	}

	firstDetection := !d.hasEmeterKnown
	if firstDetection {
		d.hasEmeter = tplink.HasEmeterFeature(sys)
		d.hasEmeterKnown = true

		// HA discovery: publish now that model + capability are known.
		if m.discovery != nil {
			m.discovery.Publish(d.Cfg.Name, sys, d.hasEmeter)
		}

		// If the first poll happened to be a sysinfo-only exchange (because
		// hasEmeter was false at request time) but the plug is actually HS110,
		// we still lack emeter values. Do one immediate additional poll so
		// the first published state carries the measurements.
		if d.hasEmeter && emeter == nil {
			sys2, em2, err2 := d.Client.Poll(ctx, true)
			if err2 == nil {
				sys = sys2
				emeter = em2
			}
			// If the re-poll fails, we still publish the sysinfo-only state
			// this tick and pick up emeter next tick.
		}
	}

	payload, err := MarshalState(d.hasEmeter, sys, emeter)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	if !equalBytes(payload, d.lastStateJSON) {
		topic := fmt.Sprintf("%s/%s/state", m.basePrefix, d.Cfg.Name)
		m.mqtt.Publish(topic, payload, m.cfg.MQTT.Retain)
		d.lastStateJSON = payload
		logger.Debug("Published state", "device", d.Cfg.Name, "payload", string(payload))
	}
	return nil
}

// publishAvailability writes online/offline to `{prefix}/{name}/available`
// only when the availability state changes (or on the first ever call for a
// device).
func (m *Manager) publishAvailability(d *Device, online bool) {
	if d.availableInitial && d.available == online {
		return
	}
	d.available = online
	d.availableInitial = true

	value := "offline"
	if online {
		value = "online"
	}
	topic := fmt.Sprintf("%s/%s/available", m.basePrefix, d.Cfg.Name)
	m.mqtt.Publish(topic, []byte(value), m.cfg.MQTT.Retain)
	logger.Info("Device availability", "device", d.Cfg.Name, "state", value)
}

// handleMessage routes an incoming MQTT message on `{prefix}/{name}/{action}`
// to the right device. Unknown devices are logged at debug and dropped.
func (m *Manager) handleMessage(topic string, payload []byte) {
	name, action, ok := parseTopic(m.basePrefix, topic)
	if !ok {
		logger.Debug("Ignoring unmatched topic", "topic", topic)
		return
	}

	m.mu.RLock()
	d, exists := m.devices[name]
	m.mu.RUnlock()
	if !exists {
		logger.Debug("Ignoring message for unknown device", "device", name, "topic", topic)
		return
	}

	switch action {
	case "get":
		if d.lastStateJSON == nil {
			logger.Debug("Get before first poll — nothing cached", "device", name)
			return
		}
		out := fmt.Sprintf("%s/%s/state", m.basePrefix, name)
		m.mqtt.Publish(out, d.lastStateJSON, m.cfg.MQTT.Retain)
	case "set":
		on, err := ParseSetCommand(payload)
		if err != nil {
			logger.Error("Invalid /set payload", "device", name, "error", err, "payload", string(payload))
			return
		}
		// Use a fresh context — MQTT callbacks are async and there's no
		// natural parent to cancel from.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.Client.SetRelay(ctx, on); err != nil {
			logger.Error("SetRelay failed", "device", name, "on", on, "error", err)
			return
		}
		// Re-poll immediately so the new state gets published without
		// waiting for the next tick.
		if err := m.pollOnce(ctx, d); err != nil {
			logger.Warn("Re-poll after /set failed", "device", name, "error", err)
		}
	}
}

// parseTopic splits `{prefix}/{name}/{action}` — returns (name, action, true)
// or ("","", false) if the topic does not match.
func parseTopic(prefix, topic string) (string, string, bool) {
	if !strings.HasPrefix(topic, prefix+"/") {
		return "", "", false
	}
	tail := topic[len(prefix)+1:]
	parts := strings.SplitN(tail, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func equalBytes(a, b []byte) bool {
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
