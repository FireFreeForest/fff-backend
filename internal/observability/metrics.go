package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	MQTTMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fff_mqtt_messages_total",
		Help: "Total MQTT messages processed by type and status.",
	}, []string{"type", "status"})

	MQTTIngestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fff_mqtt_ingest_seconds",
		Help:    "MQTT message processing duration.",
		Buckets: prometheus.DefBuckets,
	}, []string{"type"})

	RiskEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fff_risk_events_total",
		Help: "Total events emitted by the risk engine.",
	}, []string{"type", "severity"})

	CommandsIssued = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fff_commands_issued_total",
		Help: "Total commands issued.",
	}, []string{"origin", "action"})

	OutboxPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fff_outbox_published_total",
		Help: "Total outbox publish attempts.",
	}, []string{"status"})

	WSConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fff_ws_connections",
		Help: "Current WebSocket connections.",
	})

	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fff_http_requests_total",
		Help: "Total HTTP requests.",
	}, []string{"method", "path", "status"})

	NotificationsSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fff_notifications_total",
		Help: "Push notification send results.",
	}, []string{"result"})

	MCPToolCalls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fff_mcp_tool_calls_total",
		Help: "Total MCP tool calls by tool, role, and status.",
	}, []string{"tool", "role", "status"})

	MCPToolDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fff_mcp_tool_duration_seconds",
		Help:    "MCP tool call duration.",
		Buckets: prometheus.DefBuckets,
	}, []string{"tool", "role"})

	MCPAuthFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fff_mcp_auth_failures_total",
		Help: "MCP authentication and rate limit failures.",
	}, []string{"reason"})
)
