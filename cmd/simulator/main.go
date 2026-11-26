package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"firefreeforest/backend/internal/platform/logger"
)

type envPayload struct {
	DeviceID string `json:"device_id"`
	Sensors  struct {
		Temperature  float64 `json:"temperature"`
		Humidity     float64 `json:"humidity"`
		SoilMoisture float64 `json:"soil_moisture"`
	} `json:"sensors"`
	Timestamp string `json:"timestamp"`
}

type firePayload struct {
	DeviceID string `json:"device_id"`
	Sensors  struct {
		FlameDetected bool `json:"flame_detected"`
		FlameLevel    int  `json:"flame_level"`
		SmokeLevel    int  `json:"smoke_level"`
	} `json:"sensors"`
	Actuators struct {
		Pump   bool `json:"pump"`
		Buzzer bool `json:"buzzer"`
	} `json:"actuators"`
	Timestamp string `json:"timestamp"`
}

type commandMessage struct {
	DeviceID  string `json:"device_id"`
	CommandID string `json:"command_id"`
	Action    string `json:"action"`
	Payload   struct {
		State    bool `json:"state"`
		Duration int  `json:"duration"`
	} `json:"payload"`
}

type devicePayload struct {
	DeviceID        string `json:"device_id"`
	FirmwareVersion string `json:"firmware_version"`
	Wifi            struct {
		RSSI int `json:"rssi"`
	} `json:"wifi"`
	Battery struct {
		Voltage    float64 `json:"voltage"`
		Percentage int     `json:"percentage"`
	} `json:"battery"`
	Timestamp string `json:"timestamp"`
}

type ackPayload struct {
	DeviceID  string `json:"device_id"`
	CommandID string `json:"command_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	device := flag.String("device", envOr("DEVICE_ID", "FFF-VIRTUAL-DEVICE"), "device id")
	broker := flag.String("broker", envOr("MQTT_BROKER", "tcp://localhost:1883"), "mqtt broker url")
	username := flag.String("username", envOr("MQTT_DEVICE_USERNAME", ""), "shared mqtt device username")
	password := flag.String("password", envOr("MQTT_DEVICE_PASSWORD", ""), "shared mqtt device password")
	prefix := flag.String("prefix", envOr("MQTT_TOPIC_PREFIX", "fire-free-forest"), "mqtt topic prefix")
	envInterval := flag.Duration("env-interval", 15*time.Minute, "environment telemetry interval")
	fireInterval := flag.Duration("fire-interval", 5*time.Second, "fire telemetry interval")
	deviceInterval := flag.Duration("device-interval", time.Minute, "device telemetry interval")
	flame := flag.Bool("fire", false, "simulate flame detected")
	smoke := flag.Int("smoke", 180, "baseline smoke level (0-1023)")
	temp := flag.Float64("temp", 28, "baseline temperature celsius")
	humidity := flag.Float64("humidity", 65, "baseline humidity percent")
	soil := flag.Float64("soil", 640, "baseline soil sensor ADC (0-1023)")
	firmware := flag.String("firmware", envOr("DEVICE_FIRMWARE", "1.3.0"), "reported firmware version")
	rssi := flag.Int("rssi", -49, "baseline wifi RSSI (dBm)")
	batteryVoltage := flag.Float64("battery-voltage", 3.45, "baseline battery voltage")
	batteryPercentage := flag.Int("battery-percentage", 82, "baseline battery percentage (0-100)")
	flag.Parse()
	if *device == "" || *username == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "-device, shared MQTT username, and password are required; use make run-sim to load .env")
		os.Exit(2)
	}

	log := logger.New("info", "text")

	var (
		mu        sync.Mutex
		pumpOn    bool
		flameOn   = *flame
		smokeBase = *smoke
	)

	opts := paho.NewClientOptions().
		AddBroker(*broker).
		SetClientID(*device).
		SetUsername(*username).
		SetPassword(*password).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	client := paho.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		fmt.Fprintf(os.Stderr, "failed to connect mqtt: %v\n", token.Error())
		os.Exit(1)
	}
	defer client.Disconnect(250)
	log.Info("simulator connected", "broker", *broker, "device_id", *device)

	commandTopic := fmt.Sprintf("%s/%s/command", *prefix, *device)
	ackTopic := fmt.Sprintf("%s/%s/command/ack", *prefix, *device)

	publishEnv := func() {
		var p envPayload
		p.DeviceID = *device
		p.Sensors.Temperature = jitter(*temp, 0.8)
		p.Sensors.Humidity = clamp(jitter(*humidity, 2), 0, 100)
		p.Sensors.SoilMoisture = clamp(jitter(*soil, 2), 0, 1023)
		p.Timestamp = time.Now().UTC().Format(time.RFC3339)
		publishJSON(client, fmt.Sprintf("%s/%s/telemetry/environment", *prefix, *device), p, log)
	}
	publishFire := func() {
		mu.Lock()
		on := pumpOn
		f := flameOn
		mu.Unlock()
		var p firePayload
		p.DeviceID = *device
		p.Sensors.FlameDetected = f
		p.Sensors.FlameLevel = 900
		if f {
			p.Sensors.FlameLevel = 450
		}
		level := jitterInt(smokeBase, 8)
		if f {
			level = max(level, 700)
		}
		p.Sensors.SmokeLevel = clampInt(level, 0, 1023)
		p.Actuators.Pump = on
		p.Actuators.Buzzer = f || p.Sensors.SmokeLevel >= 500
		p.Timestamp = time.Now().UTC().Format(time.RFC3339)
		publishJSON(client, fmt.Sprintf("%s/%s/telemetry/fire", *prefix, *device), p, log)
	}
	publishDevice := func() {
		var p devicePayload
		p.DeviceID = *device
		p.FirmwareVersion = *firmware
		p.Wifi.RSSI = jitterInt(*rssi, 3)
		p.Battery.Voltage = math.Round(jitter(*batteryVoltage, 0.05)*1000) / 1000
		p.Battery.Percentage = clampInt(jitterInt(*batteryPercentage, 2), 0, 100)
		p.Timestamp = time.Now().UTC().Format(time.RFC3339)
		publishJSON(client, fmt.Sprintf("%s/%s/telemetry/device", *prefix, *device), p, log)
	}

	commandHandler := func(_ paho.Client, msg paho.Message) {
		var cmd commandMessage
		if err := json.Unmarshal(msg.Payload(), &cmd); err != nil {
			log.Warn("invalid command payload", "error", err)
			return
		}
		log.Info("command received", "command_id", cmd.CommandID, "action", cmd.Action)
		switch cmd.Action {
		case "SET_PUMP":
			mu.Lock()
			pumpOn = cmd.Payload.State
			mu.Unlock()
			message := "Pump deactivated"
			if cmd.Payload.State {
				message = "Pump activated"
			}
			publishAck(client, *device, ackTopic, cmd.CommandID, "success", message)
			if cmd.Payload.State && cmd.Payload.Duration > 0 {
				go func(commandID string, d int) {
					time.Sleep(time.Duration(d) * time.Second)
					mu.Lock()
					pumpOn = false
					mu.Unlock()
					log.Info("pump auto-off after duration", "command_id", commandID, "duration", d)
				}(cmd.CommandID, cmd.Payload.Duration)
			}
		case "GET_ALL_TELEMETRY":
			// Publish every telemetry stream before acking: the backend uses the
			// ack as the completion signal for its event metadata snapshot.
			publishEnv()
			publishFire()
			publishDevice()
			publishAck(client, *device, ackTopic, cmd.CommandID, "success", "All telemetry published")
		default:
			publishAck(client, *device, ackTopic, cmd.CommandID, "failed", "unsupported action")
		}
	}
	if token := client.Subscribe(commandTopic, 1, commandHandler); !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		fmt.Fprintf(os.Stderr, "failed to subscribe %s: %v\n", commandTopic, token.Error())
		os.Exit(1)
	}

	envTicker := time.NewTicker(*envInterval)
	fireTicker := time.NewTicker(*fireInterval)
	deviceTicker := time.NewTicker(*deviceInterval)
	defer envTicker.Stop()
	defer fireTicker.Stop()
	defer deviceTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	publishEnv()
	publishFire()
	publishDevice()

	for {
		select {
		case <-sigCh:
			log.Info("simulator stopping")
			return
		case <-envTicker.C:
			publishEnv()
		case <-fireTicker.C:
			publishFire()
		case <-deviceTicker.C:
			publishDevice()
		}
	}
}

func publishJSON(client paho.Client, topic string, payload any, log interface{ Info(string, ...any) }) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	token := client.Publish(topic, 1, false, raw)
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		log.Info("publish failed", "topic", topic, "error", token.Error())
		return
	}
	log.Info("telemetry published", "topic", topic)
}

func publishAck(client paho.Client, device, topic, commandID, status, message string) {
	ack := ackPayload{DeviceID: device, CommandID: commandID, Status: status, Message: message}
	raw, _ := json.Marshal(ack)
	client.Publish(topic, 1, false, raw)
}

func jitter(base, delta float64) float64 {
	v := base + (rand.Float64()*2-1)*delta
	return math.Round(v*10) / 10
}

func jitterInt(base, delta int) int {
	return base + rand.Intn(delta*2+1) - delta
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
