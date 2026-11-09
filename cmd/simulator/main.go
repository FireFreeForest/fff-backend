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
	device := flag.String("device", envOr("DEVICE_ID", "DEVICE-123"), "device id")
	broker := flag.String("broker", envOr("MQTT_BROKER", "tcp://localhost:1883"), "mqtt broker url")
	username := flag.String("username", envOr("MQTT_USERNAME", envOr("DEVICE_ID", "DEVICE-123")), "mqtt username")
	password := flag.String("password", envOr("MQTT_PASSWORD", "device123"), "mqtt password")
	envInterval := flag.Duration("env-interval", 15*time.Minute, "environment telemetry interval")
	fireInterval := flag.Duration("fire-interval", 5*time.Second, "fire telemetry interval")
	flame := flag.Bool("fire", false, "simulate flame detected")
	smoke := flag.Int("smoke", 120, "baseline smoke level (0-4095)")
	temp := flag.Float64("temp", 32.5, "baseline temperature celsius")
	humidity := flag.Float64("humidity", 68.2, "baseline humidity percent")
	soil := flag.Float64("soil", 50, "baseline soil moisture percent")
	flag.Parse()

	log := logger.New("info", "text")

	var (
		mu        sync.Mutex
		pumpOn    bool
		flameOn   = *flame
		smokeBase = *smoke
	)

	opts := paho.NewClientOptions().
		AddBroker(*broker).
		SetClientID("sim-" + *device).
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

	commandTopic := fmt.Sprintf("free-flame-forest/%s/command", *device)
	ackTopic := fmt.Sprintf("free-flame-forest/%s/command/ack", *device)

	commandHandler := func(_ paho.Client, msg paho.Message) {
		var cmd commandMessage
		if err := json.Unmarshal(msg.Payload(), &cmd); err != nil {
			log.Warn("invalid command payload", "error", err)
			return
		}
		log.Info("command received", "command_id", cmd.CommandID, "action", cmd.Action, "state", cmd.Payload.State, "duration", cmd.Payload.Duration)
		if cmd.Action != "SET_PUMP" {
			publishAck(client, *device, ackTopic, cmd.CommandID, "failed", "unsupported action")
			return
		}
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
	}
	if token := client.Subscribe(commandTopic, 1, commandHandler); !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		fmt.Fprintf(os.Stderr, "failed to subscribe %s: %v\n", commandTopic, token.Error())
		os.Exit(1)
	}

	envTicker := time.NewTicker(*envInterval)
	fireTicker := time.NewTicker(*fireInterval)
	defer envTicker.Stop()
	defer fireTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	publishEnv := func() {
		var p envPayload
		p.DeviceID = *device
		p.Sensors.Temperature = jitter(*temp, 0.8)
		p.Sensors.Humidity = clamp(jitter(*humidity, 2), 0, 100)
		p.Sensors.SoilMoisture = clamp(jitter(*soil, 2), 0, 100)
		p.Timestamp = time.Now().UTC().Format(time.RFC3339)
		publishJSON(client, fmt.Sprintf("free-flame-forest/%s/telemetry/environment", *device), p, log)
	}
	publishFire := func() {
		mu.Lock()
		on := pumpOn
		f := flameOn
		mu.Unlock()
		var p firePayload
		p.DeviceID = *device
		p.Sensors.FlameDetected = f
		level := jitterInt(smokeBase, 8)
		if f {
			level = max(level, 700)
		}
		p.Sensors.SmokeLevel = clampInt(level, 0, 4095)
		p.Actuators.Pump = on
		p.Actuators.Buzzer = f
		p.Timestamp = time.Now().UTC().Format(time.RFC3339)
		publishJSON(client, fmt.Sprintf("free-flame-forest/%s/telemetry/fire", *device), p, log)
	}

	publishEnv()
	publishFire()

	for {
		select {
		case <-sigCh:
			log.Info("simulator stopping")
			return
		case <-envTicker.C:
			publishEnv()
		case <-fireTicker.C:
			publishFire()
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
