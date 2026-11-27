package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"firefreeforest/backend/internal/domain"
)

type Config struct {
	Env      string
	HTTPAddr string
	LogLevel string

	DBDSN string

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	GeocodeBaseURL   string
	GeocodeUserAgent string
	GeocodeCacheTTL  time.Duration

	MQTTBroker      string
	MQTTClientID    string
	MQTTUsername    string
	MQTTPassword    string
	MQTTQoS         int
	MQTTTopicPrefix string

	JWTSecret  string
	AccessTTL  time.Duration
	RefreshTTL time.Duration

	CORSOrigins []string

	MigrateOnStart      bool
	AutoRegisterDevices bool
	SeedAdminEmail      string
	SeedAdminPassword   string
	SeedAdminName       string
	SeedDeviceID        string
	SeedDeviceName      string

	TemperatureHighC  float64
	SoilDryADC        float64
	SmokeLevelMax     int
	BatteryLowPercent int
	EventCooldown     time.Duration
	FireCooldown      time.Duration

	TelemetryRefreshTimeout  time.Duration
	TelemetryRefreshThrottle time.Duration

	AutoPumpEnabled  bool
	AutoPumpDuration time.Duration
	AutoPumpCooldown time.Duration
	AutoPumpMax      time.Duration

	OfflineAfter    time.Duration
	CommandTimeout  time.Duration
	RetentionDays   int
	WSAllowedOrigin string
	LoginRatePerMin int

	FCMCredentialsFile string
	FCMProjectID       string
	FCMMinSeverity     string
	FCMEventTypes      []string

	MCPRateLimitPerMin int
	MCPAPIKey          string

	MastraURL                string
	AssistantRateLimitPerMin int
	AssistantMaxConcurrent   int
}

func Load() (*Config, error) { return load(true) }

// LoadMCP loads configuration for the stdio MCP binary. Unlike Load, it does
// not require JWT_SECRET because the stdio transport does not authenticate
// HTTP users.
func LoadMCP() (*Config, error) { return load(false) }

// LoadMigrate loads configuration for the standalone migration binary. It only
// needs the database DSN, so JWT_SECRET is not required.
func LoadMigrate() (*Config, error) { return load(false) }

func load(requireJWT bool) (*Config, error) {
	cfg := &Config{
		Env:      env("APP_ENV", "development"),
		HTTPAddr: env("HTTP_ADDR", ":8080"),
		LogLevel: env("LOG_LEVEL", "info"),

		DBDSN: env("DB_DSN", ""),

		RedisAddr:     env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: env("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		GeocodeBaseURL:   env("GEOCODE_BASE_URL", "https://nominatim.openstreetmap.org"),
		GeocodeUserAgent: env("GEOCODE_USER_AGENT", "FireFreeForest/1.0"),
		GeocodeCacheTTL:  envDuration("GEOCODE_CACHE_TTL", 30*24*time.Hour),

		MQTTBroker:      env("MQTT_BROKER", "tcp://localhost:1883"),
		MQTTClientID:    env("MQTT_CLIENT_ID", ""),
		MQTTUsername:    env("MQTT_USERNAME", "backend"),
		MQTTPassword:    env("MQTT_PASSWORD", "change-me-backend"),
		MQTTQoS:         envInt("MQTT_QOS", 1),
		MQTTTopicPrefix: env("MQTT_TOPIC_PREFIX", "fire-free-forest"),

		JWTSecret:  env("JWT_SECRET", ""),
		AccessTTL:  envDuration("JWT_ACCESS_TTL", 15*time.Minute),
		RefreshTTL: envDuration("JWT_REFRESH_TTL", 7*24*time.Hour),

		CORSOrigins: envCSV("CORS_ORIGINS", "http://localhost:3000,http://localhost:5173"),

		MigrateOnStart:      envBool("MIGRATE_ON_START", true),
		AutoRegisterDevices: envBool("AUTO_REGISTER_DEVICES", true),
		SeedAdminEmail:      env("SEED_ADMIN_EMAIL", "admin@fff.local"),
		SeedAdminPassword:   env("SEED_ADMIN_PASSWORD", "admin12345"),
		SeedAdminName:       env("SEED_ADMIN_NAME", "Administrator"),
		SeedDeviceID:        env("SEED_DEVICE_ID", "FFF-VIRTUAL-DEVICE"),
		SeedDeviceName:      env("SEED_DEVICE_NAME", "Simulator Device"),

		TemperatureHighC:  envFloat("THRESHOLD_TEMPERATURE_HIGH_C", 40),
		SoilDryADC:        envFloat("THRESHOLD_SOIL_DRY_ADC", 300),
		SmokeLevelMax:     envInt("THRESHOLD_SMOKE_LEVEL", 500),
		BatteryLowPercent: envInt("THRESHOLD_BATTERY_LOW_PERCENT", 20),
		EventCooldown:     envDuration("EVENT_COOLDOWN", 5*time.Minute),
		FireCooldown:      envDuration("FIRE_EVENT_COOLDOWN", time.Minute),

		TelemetryRefreshTimeout:  envDuration("TELEMETRY_REFRESH_TIMEOUT", 5*time.Second),
		TelemetryRefreshThrottle: envDuration("TELEMETRY_REFRESH_THROTTLE", 30*time.Second),

		AutoPumpEnabled:  envBool("AUTO_PUMP_ENABLED", false),
		AutoPumpDuration: envDuration("AUTO_PUMP_DURATION", 30*time.Second),
		AutoPumpCooldown: envDuration("AUTO_PUMP_COOLDOWN", 5*time.Minute),
		AutoPumpMax:      envDuration("AUTO_PUMP_MAX_DURATION", 300*time.Second),

		OfflineAfter:    envDuration("DEVICE_OFFLINE_AFTER", 30*time.Second),
		CommandTimeout:  envDuration("COMMAND_TIMEOUT", 60*time.Second),
		RetentionDays:   envInt("TELEMETRY_RETENTION_DAYS", 90),
		WSAllowedOrigin: env("WS_ALLOWED_ORIGIN", "*"),
		LoginRatePerMin: envInt("LOGIN_RATE_LIMIT_PER_MIN", 10),

		FCMCredentialsFile: env("FCM_CREDENTIALS_FILE", ""),
		FCMProjectID:       env("FCM_PROJECT_ID", ""),
		FCMMinSeverity:     env("FCM_MIN_SEVERITY", "error"),
		FCMEventTypes: envCSV(
			"FCM_EVENT_TYPES",
			"FIRE_DETECTED,SMOKE_DETECTED,TEMPERATURE_HIGH,SOIL_DRY,DEVICE_OFFLINE,SENSOR_ERROR,LOW_BATTERY",
		),

		MCPRateLimitPerMin: envInt("MCP_RATE_LIMIT_PER_MIN", 120),
		MCPAPIKey:          env("MCP_API_KEY", ""),

		MastraURL:                env("MASTRA_URL", ""),
		AssistantRateLimitPerMin: envInt("ASSISTANT_RATE_LIMIT_PER_MIN", 20),
		AssistantMaxConcurrent:   envInt("ASSISTANT_MAX_CONCURRENT_STREAMS", 2),
	}

	if cfg.DBDSN == "" {
		return nil, fmt.Errorf("DB_DSN is required")
	}
	if requireJWT {
		if cfg.JWTSecret == "" {
			return nil, fmt.Errorf("JWT_SECRET is required")
		}
		if len(cfg.JWTSecret) < 24 {
			return nil, fmt.Errorf("JWT_SECRET must be at least 24 characters")
		}
	}
	if cfg.MQTTQoS < 0 || cfg.MQTTQoS > 2 {
		return nil, fmt.Errorf("MQTT_QOS must be 0, 1, or 2")
	}
	if cfg.MQTTTopicPrefix == "" || strings.ContainsAny(cfg.MQTTTopicPrefix, "/+#") {
		return nil, fmt.Errorf("MQTT_TOPIC_PREFIX must be a non-empty single topic segment")
	}
	if cfg.AutoPumpDuration <= 0 || cfg.AutoPumpDuration > cfg.AutoPumpMax {
		return nil, fmt.Errorf("AUTO_PUMP_DURATION must be between 1s and AUTO_PUMP_MAX_DURATION")
	}
	if cfg.OfflineAfter < 5*time.Second {
		return nil, fmt.Errorf("DEVICE_OFFLINE_AFTER must be at least 5s")
	}
	switch strings.ToLower(cfg.FCMMinSeverity) {
	case "error", "warning", "critical":
	default:
		return nil, fmt.Errorf("FCM_MIN_SEVERITY must be error, warning, or critical")
	}
	cfg.FCMMinSeverity = strings.ToLower(cfg.FCMMinSeverity)

	normalizedEvents := make([]string, 0, len(cfg.FCMEventTypes))
	seenEvents := make(map[domain.EventType]struct{}, len(cfg.FCMEventTypes))
	for _, raw := range cfg.FCMEventTypes {
		eventType := domain.EventType(strings.ToUpper(strings.TrimSpace(raw)))
		if !domain.ValidEventType(eventType) {
			return nil, fmt.Errorf("FCM_EVENT_TYPES contains invalid event type %q", raw)
		}
		if _, seen := seenEvents[eventType]; seen {
			continue
		}
		seenEvents[eventType] = struct{}{}
		normalizedEvents = append(normalizedEvents, string(eventType))
	}
	cfg.FCMEventTypes = normalizedEvents

	if cfg.MCPRateLimitPerMin < 0 {
		return nil, fmt.Errorf("MCP_RATE_LIMIT_PER_MIN must be zero or greater")
	}
	if cfg.AssistantRateLimitPerMin < 0 {
		return nil, fmt.Errorf("ASSISTANT_RATE_LIMIT_PER_MIN must be zero or greater")
	}
	if cfg.AssistantMaxConcurrent < 0 {
		return nil, fmt.Errorf("ASSISTANT_MAX_CONCURRENT_STREAMS must be zero or greater")
	}

	return cfg, nil
}

func (c *Config) IsProduction() bool { return c.Env == "production" }

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

func envCSV(key, def string) []string {
	raw := env(key, def)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
