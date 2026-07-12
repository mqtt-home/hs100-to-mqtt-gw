package hadiscovery

import (
	"sync"

	"github.com/mqtt-home/hs100-to-mqtt-gw/tplink"
	"github.com/philipparndt/go-logger"
)

// Publisher owns the Home Assistant MQTT discovery lifecycle: it publishes
// retained discovery configs for each known device and clears them on
// graceful shutdown. Safe for concurrent use from multiple device goroutines.
type Publisher struct {
	basePrefix string
	pub        func(topic string, payload []byte, retain bool)

	mu     sync.Mutex
	topics map[string]struct{} // set of topics we have published
}

// NewPublisher builds a Publisher that writes to the given publish function.
// `basePrefix` is the MQTT topic prefix used by the device state topics
// (e.g. `hs100`) — needed so discovery payloads reference the correct
// `{prefix}/{name}/state` topics.
func NewPublisher(basePrefix string, publish func(topic string, payload []byte, retain bool)) *Publisher {
	return &Publisher{
		basePrefix: basePrefix,
		pub:        publish,
		topics:     make(map[string]struct{}),
	}
}

// Publish emits the discovery configs for one device. Idempotent: re-invoking
// for the same device replaces the payloads without growing the tracked set.
// Retained payloads are always published (HA discovery requires retain).
func (p *Publisher) Publish(deviceName string, sys tplink.SysInfo, hasEmeter bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Switch entity (always present).
	if topic, payload, err := SwitchConfig(p.basePrefix, deviceName, sys); err != nil {
		logger.Error("HA discovery: switch config marshal failed", "device", deviceName, "error", err)
	} else {
		p.pub(topic, payload, true)
		p.topics[topic] = struct{}{}
	}

	// Sensor entities (HS110 only).
	if !hasEmeter {
		return
	}
	for _, m := range AllMetrics {
		topic, payload, err := SensorConfig(p.basePrefix, deviceName, sys, m)
		if err != nil {
			logger.Error("HA discovery: sensor config marshal failed", "device", deviceName, "metric", m, "error", err)
			continue
		}
		p.pub(topic, payload, true)
		p.topics[topic] = struct{}{}
	}
}

// Cleanup clears every discovery topic ever published by this Publisher
// (empty retained payload → HA removes the entity). Called from the graceful
// shutdown path before the MQTT client disconnects.
func (p *Publisher) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for topic := range p.topics {
		p.pub(topic, []byte(""), true)
	}
	// Reset so a re-invocation after a hot-reload does not double-clear.
	p.topics = make(map[string]struct{})
}

// TrackedTopics returns a snapshot of the currently tracked discovery topics.
// Test-only surface — not for callers to depend on in production.
func (p *Publisher) TrackedTopics() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.topics))
	for t := range p.topics {
		out = append(out, t)
	}
	return out
}
