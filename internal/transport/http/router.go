package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
	"firefreeforest/backend/internal/transport/ws"
)

type Deps struct {
	Config   *config.Config
	Repo     *repository.Repository
	Auth     *service.AuthService
	Commands *service.CommandService
	Geocoder *service.Geocoder
	Hub      *ws.Hub
	Redis    *goredis.Client
	Logger   *slog.Logger
}

type Handler struct {
	cfg      *config.Config
	repo     *repository.Repository
	auth     *service.AuthService
	commands *service.CommandService
	geocoder *service.Geocoder
	hub      *ws.Hub
	rdb      *goredis.Client
	log      *slog.Logger
}

func NewRouter(d Deps) *gin.Engine {
	if d.Config.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	h := &Handler{
		cfg:      d.Config,
		repo:     d.Repo,
		auth:     d.Auth,
		commands: d.Commands,
		geocoder: d.Geocoder,
		hub:      d.Hub,
		rdb:      d.Redis,
		log:      d.Logger,
	}

	r := gin.New()
	r.Use(requestIDMiddleware(), loggingMiddleware(d.Logger), recoveryMiddleware(d.Logger), corsMiddleware(d.Config.CORSOrigins), metricsMiddleware())

	r.GET("/healthz", h.health)
	r.GET("/readyz", h.ready)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	registerDocsRoutes(r)

	v1 := r.Group("/api/v1")
	v1.POST("/auth/login", loginRateLimit(d.Redis, d.Config.LoginRatePerMin), h.login)
	v1.POST("/auth/refresh", h.refresh)
	v1.GET("/ws", h.websocket)
	v1.POST("/realtime/ticket", authMiddleware(d.Auth), h.wsTicket)

	secured := v1.Group("")
	secured.Use(authMiddleware(d.Auth))
	secured.GET("/auth/me", h.me)
	secured.POST("/auth/logout", h.logout)

	secured.GET("/devices", h.listDevices)
	secured.GET("/devices/:deviceID", h.getDevice)
	secured.GET("/devices/:deviceID/status", h.deviceStatus)
	secured.GET("/devices/:deviceID/telemetry/environment", h.listEnvironment)
	secured.GET("/devices/:deviceID/telemetry/environment/aggregate", h.aggregateEnvironment)
	secured.GET("/devices/:deviceID/telemetry/fire", h.listFire)
	secured.GET("/devices/:deviceID/telemetry/fire/aggregate", h.aggregateFire)
	secured.GET("/devices/:deviceID/telemetry/latest", h.latestTelemetry)
	secured.GET("/devices/:deviceID/commands", h.listCommands)
	secured.GET("/commands/:commandID", h.getCommand)
	secured.GET("/events", h.listEvents)
	secured.GET("/events/:eventID", h.getEvent)
	secured.GET("/events/:eventID/timeline", h.listEventTimeline)
	secured.GET("/dashboard/summary", h.dashboardSummary)
	secured.GET("/geocode/reverse", h.reverseGeocode)
	secured.POST("/push/tokens", h.registerPushToken)
	secured.DELETE("/push/tokens/:token", h.deletePushToken)

	operator := secured.Group("")
	operator.Use(requireRoles(domain.RoleAdmin, domain.RolePetugas))
	operator.POST("/devices/:deviceID/commands", h.issueCommand)
	operator.POST("/events/:eventID/acknowledge", h.acknowledgeEvent)

	petugas := secured.Group("")
	petugas.Use(requireRoles(domain.RolePetugas))
	petugas.POST("/events/:eventID/resolve", h.resolveEvent)

	admin := secured.Group("")
	admin.Use(requireRoles(domain.RoleAdmin))
	admin.POST("/devices", h.createDevice)
	admin.PATCH("/devices/:deviceID", h.updateDevice)
	admin.DELETE("/devices/:deviceID", h.deleteDevice)
	admin.GET("/users", h.listUsers)
	admin.POST("/users", h.createUser)
	admin.PATCH("/users/:userID", h.updateUser)

	return r
}

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	components := gin.H{}
	ready := true
	if err := h.repo.Ping(ctx); err != nil {
		components["postgres"] = "down"
		ready = false
	} else {
		components["postgres"] = "ok"
	}
	if err := h.rdb.Ping(ctx).Err(); err != nil {
		components["redis"] = "down"
		ready = false
	} else {
		components["redis"] = "ok"
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"ready": ready, "components": components})
}
