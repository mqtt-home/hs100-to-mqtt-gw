package bridge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mqtt-home/hs100-to-mqtt-gw/config"
	"github.com/mqtt-home/hs100-to-mqtt-gw/hadiscovery"
	"github.com/mqtt-home/hs100-to-mqtt-gw/tplink"
)

// --- test doubles ---

type fakePublish struct {
	topic   string
	payload []byte
	retain  bool
}

type fakeMQTT struct {
	mu       sync.Mutex
	subs     map[string]func(topic string, payload []byte)
	messages []fakePublish
}

func newFakeMQTT() *fakeMQTT {
	return &fakeMQTT{subs: make(map[string]func(topic string, payload []byte))}
}

func (f *fakeMQTT) Publish(topic string, payload []byte, retain bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, fakePublish{topic: topic, payload: append([]byte(nil), payload...), retain: retain})
}

func (f *fakeMQTT) Subscribe(topic string, handler func(topic string, payload []byte)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[topic] = handler
}

func (f *fakeMQTT) deliver(topic string, payload []byte) {
	f.mu.Lock()
	// Very small pattern-match: the subscription pattern is `{prefix}/+/{action}`.
	// We iterate every subscription and match a leading + as a wildcard.
	var matched []func(string, []byte)
	for pattern, handler := range f.subs {
		if match(pattern, topic) {
			matched = append(matched, handler)
		}
	}
	f.mu.Unlock()
	for _, h := range matched {
		h(topic, payload)
	}
}

func match(pattern, topic string) bool {
	pparts := strings.Split(pattern, "/")
	tparts := strings.Split(topic, "/")
	if len(pparts) != len(tparts) {
		return false
	}
	for i, p := range pparts {
		if p == "+" {
			continue
		}
		if p != tparts[i] {
			return false
		}
	}
	return true
}

func (f *fakeMQTT) snapshotMessages() []fakePublish {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakePublish, len(f.messages))
	copy(out, f.messages)
	return out
}

// fakePoller is a scripted Poller. Behaviour is driven by three fields.
type fakePoller struct {
	mu             sync.Mutex
	relayOn        bool
	feature        string // "TIM" or "TIM:ENE"
	emeterOnAllPolls bool // controls whether the poller emits emeter when wantEmeter=true

	// power reading the poller emits (fixed for tests)
	powerW float64

	// counters
	pollCount    int
	setRelayCalls []bool
	pollErr       error
}

func newFakePoller(feature string) *fakePoller {
	return &fakePoller{feature: feature, emeterOnAllPolls: true, powerW: 42.0}
}

func (p *fakePoller) Poll(ctx context.Context, wantEmeter bool) (tplink.SysInfo, *tplink.EmeterRealtime, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pollCount++
	if p.pollErr != nil {
		return tplink.SysInfo{}, nil, p.pollErr
	}
	relay := 0
	if p.relayOn {
		relay = 1
	}
	sys := tplink.SysInfo{
		Model:      "HS110",
		Alias:      "test",
		SwVer:      "1.0",
		Feature:    p.feature,
		RelayState: &relay,
	}
	if !wantEmeter {
		return sys, nil, nil
	}
	if !p.emeterOnAllPolls {
		return sys, nil, nil
	}
	em := &tplink.EmeterRealtime{PowerW: p.powerW, VoltageV: 230.0, CurrentA: 0.183, EnergyKwh: 1.5}
	return sys, em, nil
}

func (p *fakePoller) SetRelay(ctx context.Context, on bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setRelayCalls = append(p.setRelayCalls, on)
	p.relayOn = on
	return nil
}

// --- test helpers ---

func makeCfg(devs ...string) config.Config {
	cfg := config.Config{
		MQTT: config.MQTTConfig{
			Topic:  "hs100",
			Retain: true,
			QoS:    1,
			URL:    "tcp://broker:1883",
		},
		PollingIntervalSeconds: 1,
	}
	for _, name := range devs {
		cfg.Devices = append(cfg.Devices, config.Device{Host: "10.0.0.1", Name: name})
	}
	return cfg
}

// --- tests ---

func TestPollOnce_HS100_DetectsAndPublishesSwitchOnly(t *testing.T) {
	cfg := makeCfg("plug")
	fp := newFakePoller("TIM") // HS100 feature — no ENE
	mq := newFakeMQTT()
	disc := hadiscovery.NewPublisher("hs100", func(topic string, payload []byte, retain bool) {
		mq.Publish(topic, payload, retain)
	})
	m := NewManagerWith(cfg, mq, disc, map[string]Poller{"plug": fp})

	d := m.devices["plug"]
	if err := m.pollOnce(context.Background(), d); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if !d.hasEmeterKnown {
		t.Fatal("hasEmeterKnown should be true after first poll")
	}
	if d.hasEmeter {
		t.Error("HS100 must not be marked as hasEmeter")
	}

	// State payload must not contain power/voltage/current/energy.
	found := false
	for _, msg := range mq.snapshotMessages() {
		if msg.topic == "hs100/plug/state" {
			found = true
			s := string(msg.payload)
			for _, k := range []string{"power_w", "voltage_v", "current_a", "energy_kwh"} {
				if strings.Contains(s, k) {
					t.Errorf("HS100 state payload must not contain %q, got: %s", k, s)
				}
			}
		}
	}
	if !found {
		t.Fatal("no state message published")
	}

	// HA discovery: exactly one topic for HS100 (the switch).
	switchTopic := "homeassistant/switch/hs100_plug/config"
	found = false
	for _, msg := range mq.snapshotMessages() {
		if msg.topic == switchTopic && !msg.retain == false {
			found = true
		}
	}
	if !found {
		t.Error("HS100 must publish exactly one discovery topic (switch)")
	}
}

func TestPollOnce_HS110_PublishesFullStateAndFiveDiscoveryTopics(t *testing.T) {
	cfg := makeCfg("kitchen")
	fp := newFakePoller("TIM:ENE")
	mq := newFakeMQTT()
	disc := hadiscovery.NewPublisher("hs100", func(topic string, payload []byte, retain bool) {
		mq.Publish(topic, payload, retain)
	})
	m := NewManagerWith(cfg, mq, disc, map[string]Poller{"kitchen": fp})

	d := m.devices["kitchen"]
	if err := m.pollOnce(context.Background(), d); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if !d.hasEmeter {
		t.Fatal("HS110 must be marked as hasEmeter after first poll")
	}

	// HA discovery: switch + four sensors published
	wantTopics := map[string]bool{
		"homeassistant/switch/hs100_kitchen/config":         false,
		"homeassistant/sensor/hs100_kitchen_power/config":   false,
		"homeassistant/sensor/hs100_kitchen_voltage/config": false,
		"homeassistant/sensor/hs100_kitchen_current/config": false,
		"homeassistant/sensor/hs100_kitchen_energy/config":  false,
	}
	stateFound := false
	for _, msg := range mq.snapshotMessages() {
		if _, ok := wantTopics[msg.topic]; ok {
			wantTopics[msg.topic] = true
		}
		if msg.topic == "hs100/kitchen/state" {
			stateFound = true
			s := string(msg.payload)
			for _, k := range []string{"power_w", "voltage_v", "current_a", "energy_kwh"} {
				if !strings.Contains(s, k) {
					t.Errorf("HS110 state payload missing %q: %s", k, s)
				}
			}
		}
	}
	for topic, seen := range wantTopics {
		if !seen {
			t.Errorf("expected discovery topic %s not published", topic)
		}
	}
	if !stateFound {
		t.Fatal("no state message published")
	}
}

func TestPollOnce_StateDeduplication(t *testing.T) {
	cfg := makeCfg("plug")
	fp := newFakePoller("TIM:ENE")
	fp.powerW = 42.0
	mq := newFakeMQTT()
	m := NewManagerWith(cfg, mq, nil, map[string]Poller{"plug": fp})

	d := m.devices["plug"]
	if err := m.pollOnce(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	beforeSecond := 0
	for _, msg := range mq.snapshotMessages() {
		if msg.topic == "hs100/plug/state" {
			beforeSecond++
		}
	}
	// Poll again with identical values — no new state publish expected.
	if err := m.pollOnce(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	afterSecond := 0
	for _, msg := range mq.snapshotMessages() {
		if msg.topic == "hs100/plug/state" {
			afterSecond++
		}
	}
	if afterSecond != beforeSecond {
		t.Errorf("state publish count = %d, want %d (dedup)", afterSecond, beforeSecond)
	}

	// Change a value and poll again — new state must be published.
	fp.powerW = 43.5
	if err := m.pollOnce(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	afterChange := 0
	for _, msg := range mq.snapshotMessages() {
		if msg.topic == "hs100/plug/state" {
			afterChange++
		}
	}
	if afterChange != beforeSecond+1 {
		t.Errorf("state publish after change = %d, want %d", afterChange, beforeSecond+1)
	}
}

func TestPublishAvailability_OnlyOnTransition(t *testing.T) {
	cfg := makeCfg("plug")
	mq := newFakeMQTT()
	m := NewManagerWith(cfg, mq, nil, map[string]Poller{"plug": newFakePoller("TIM")})

	d := m.devices["plug"]
	m.publishAvailability(d, true)
	m.publishAvailability(d, true)  // repeat — no publish
	m.publishAvailability(d, false) // transition
	m.publishAvailability(d, false) // repeat — no publish
	m.publishAvailability(d, true)  // transition

	availCount := 0
	for _, msg := range mq.snapshotMessages() {
		if msg.topic == "hs100/plug/available" {
			availCount++
		}
	}
	if availCount != 3 {
		t.Errorf("availability publishes = %d, want 3", availCount)
	}
}

func TestHandleMessage_SetCommand(t *testing.T) {
	cfg := makeCfg("plug")
	fp := newFakePoller("TIM")
	mq := newFakeMQTT()
	m := NewManagerWith(cfg, mq, nil, map[string]Poller{"plug": fp})

	// Simulate the /set delivery via the fake broker.
	mq.subs["hs100/+/set"] = m.handleMessage
	mq.deliver("hs100/plug/set", []byte("true"))

	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.setRelayCalls) != 1 || fp.setRelayCalls[0] != true {
		t.Errorf("setRelay calls = %v, want [true]", fp.setRelayCalls)
	}
}

func TestHandleMessage_UnknownDeviceIgnored(t *testing.T) {
	cfg := makeCfg("plug")
	fp := newFakePoller("TIM")
	mq := newFakeMQTT()
	m := NewManagerWith(cfg, mq, nil, map[string]Poller{"plug": fp})

	// Should be a no-op — no panic, no set-relay call.
	m.handleMessage("hs100/unknown/set", []byte("true"))

	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.setRelayCalls) != 0 {
		t.Errorf("setRelay must not be called for unknown device, got %v", fp.setRelayCalls)
	}
}

func TestHandleMessage_GetRepublishesCachedState(t *testing.T) {
	cfg := makeCfg("plug")
	fp := newFakePoller("TIM")
	mq := newFakeMQTT()
	m := NewManagerWith(cfg, mq, nil, map[string]Poller{"plug": fp})

	d := m.devices["plug"]
	// First poll to populate the cache.
	if err := m.pollOnce(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	stateBefore := 0
	for _, msg := range mq.snapshotMessages() {
		if msg.topic == "hs100/plug/state" {
			stateBefore++
		}
	}

	m.handleMessage("hs100/plug/get", []byte(""))

	stateAfter := 0
	for _, msg := range mq.snapshotMessages() {
		if msg.topic == "hs100/plug/state" {
			stateAfter++
		}
	}
	if stateAfter != stateBefore+1 {
		t.Errorf("get did not re-publish state: before=%d after=%d", stateBefore, stateAfter)
	}
}

func TestPollOnce_ErrorPropagates(t *testing.T) {
	cfg := makeCfg("plug")
	fp := newFakePoller("TIM")
	fp.pollErr = errors.New("boom")
	mq := newFakeMQTT()
	m := NewManagerWith(cfg, mq, nil, map[string]Poller{"plug": fp})

	err := m.pollOnce(context.Background(), m.devices["plug"])
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestRun_StopsOnContextCancel(t *testing.T) {
	cfg := makeCfg("plug")
	fp := newFakePoller("TIM")
	mq := newFakeMQTT()
	m := NewManagerWith(cfg, mq, nil, map[string]Poller{"plug": fp})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	// Give the manager one tick.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
