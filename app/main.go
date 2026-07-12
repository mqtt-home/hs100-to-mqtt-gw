package main

import (
	"context"
	_ "expvar"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/mqtt-home/hs100-to-mqtt-gw/bridge"
	"github.com/mqtt-home/hs100-to-mqtt-gw/config"
	"github.com/mqtt-home/hs100-to-mqtt-gw/hadiscovery"
	"github.com/mqtt-home/hs100-to-mqtt-gw/version"
	"github.com/philipparndt/go-logger"
	"github.com/philipparndt/mqtt-gateway/mqtt"
)

// pprofAddr matches the sibling bridges — always :6060, empty disables.
const pprofAddr = ":6060"

// mqttClientIDPrefix is the client-id prefix passed to mqtt-gateway; the
// gateway appends a random suffix to make it unique per connection.
const mqttClientIDPrefix = "hs2mqtt"

func initPprof() {
	if pprofAddr == "" {
		return
	}
	go func() {
		if err := http.ListenAndServe(pprofAddr, nil); err != nil {
			logger.Error("pprof listener failed", "error", err)
		}
	}()
}

func main() {
	logger.Init("info", logger.Logger())
	logger.Info("hs100-to-mqtt-gw",
		"version", version.Version,
		"commit", version.GitCommit,
		"built", version.BuildTime,
	)

	if len(os.Args) != 2 {
		logger.Error("Expected config file as argument")
		os.Exit(1)
	}
	configFile := os.Args[1]
	logger.Info("Loading config", "file", configFile)

	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}
	logger.SetLevel(cfg.LogLevel)
	logger.Info("Config loaded", "devices", len(cfg.Devices), "prefix", cfg.MQTT.Topic)

	initPprof()

	// Connect MQTT — this blocks until the initial connection succeeds.
	// mqtt-gateway installs the LWT ({prefix}/bridge/state -> "offline") from
	// the config Topic *and* publishes the corresponding "online" on connect,
	// so no explicit bridge-status publish is needed here.
	mqtt.Start(cfg.MQTT.ToGatewayConfig(), mqttClientIDPrefix)

	// Adapter that both the manager and the discovery publisher use.
	mqttAdapter := &bridge.GatewayMQTT{Retain: cfg.MQTT.Retain}
	discovery := hadiscovery.NewPublisher(cfg.MQTT.Topic, func(topic string, payload []byte, retain bool) {
		mqttAdapter.Publish(topic, payload, retain)
	})

	mgr := bridge.NewManager(cfg, mqttAdapter, discovery)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)
	logger.Info("Application is now ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("Shutting down")

	// Cancel the manager first so device goroutines stop polling / holding
	// the driver conn, then clear HA discovery topics before the MQTT
	// client disconnects (the mqtt-gateway LWT will then publish the
	// bridge/state -> "offline" transition).
	cancel()
	discovery.Cleanup()
}
