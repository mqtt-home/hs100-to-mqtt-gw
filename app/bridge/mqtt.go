package bridge

import (
	"github.com/philipparndt/mqtt-gateway/mqtt"
)

// MQTTClient is the minimal MQTT surface the manager and discovery publisher
// need. Extracting the interface lets tests inject a fake without spinning up
// a broker.
type MQTTClient interface {
	Publish(topic string, payload []byte, retain bool)
	Subscribe(topic string, handler func(topic string, payload []byte))
}

// GatewayMQTT adapts the shared mqtt-gateway package to MQTTClient.
type GatewayMQTT struct {
	Retain bool
}

func (g *GatewayMQTT) Publish(topic string, payload []byte, retain bool) {
	mqtt.PublishAbsolute(topic, string(payload), retain)
}

func (g *GatewayMQTT) Subscribe(topic string, handler func(topic string, payload []byte)) {
	mqtt.Subscribe(topic, mqtt.OnMessageListener(handler))
}
