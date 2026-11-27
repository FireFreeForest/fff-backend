package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/platform/logger"
	"firefreeforest/backend/internal/platform/postgres"
	platformredis "firefreeforest/backend/internal/platform/redis"
	"firefreeforest/backend/internal/realtime"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
	httpapi "firefreeforest/backend/internal/transport/http"
	"firefreeforest/backend/internal/transport/ws"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logFormat := "text"
	if cfg.IsProduction() {
		logFormat = "json"
	}
	log := logger.New(cfg.LogLevel, logFormat)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.MigrateOnStart {
		if err := postgres.Migrate(cfg.DBDSN); err != nil {
			return err
		}
		log.Info("database migrations applied")
	}

	db, err := postgres.Open(cfg.DBDSN)
	if err != nil {
		return err
	}
	rdb, err := platformredis.Open(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer rdb.Close()

	repo := repository.New(db)
	auth := service.NewAuthService(repo, cfg, log)
	if err := auth.SeedAdmin(ctx); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	if err := seedDevice(ctx, cfg, repo, log); err != nil {
		return err
	}
	commands := service.NewCommandService(repo, rdb, log, cfg.AutoPumpMax, cfg.MQTTTopicPrefix)
	geocoder := service.NewGeocoder(rdb, cfg.GeocodeBaseURL, cfg.GeocodeUserAgent, cfg.GeocodeCacheTTL, log)
	if cfg.MCPAPIKey != "" {
		log.Info("mcp http endpoint enabled (read-only)", "path", "/mcp", "rate_limit_per_min", cfg.MCPRateLimitPerMin)
	}

	hub := ws.NewHub(log)
	go hub.Run(ctx)
	go subscribeRealtime(ctx, rdb, hub, log)

	router := httpapi.NewRouter(httpapi.Deps{
		Config:   cfg,
		Repo:     repo,
		Auth:     auth,
		Commands: commands,
		Geocoder: geocoder,
		Hub:      hub,
		Redis:    rdb,
		Logger:   log,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api server listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down api server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func seedDevice(ctx context.Context, cfg *config.Config, repo *repository.Repository, log *slog.Logger) error {
	if cfg.SeedDeviceID == "" {
		return nil
	}
	if _, err := repo.GetDeviceByDeviceID(ctx, cfg.SeedDeviceID); err == nil {
		return nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	now := time.Now().UTC()
	device := &domain.Device{
		ID:        uuid.New(),
		DeviceID:  cfg.SeedDeviceID,
		Name:      cfg.SeedDeviceName,
		Status:    domain.DeviceOffline,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repo.CreateDevice(ctx, device); err != nil {
		return fmt.Errorf("seed device: %w", err)
	}
	log.Info("seeded device", "device_id", device.DeviceID)
	return nil
}

func subscribeRealtime(ctx context.Context, rdb *goredis.Client, hub *ws.Hub, log *slog.Logger) {
	pubsub := rdb.Subscribe(ctx, realtime.Channel)
	defer pubsub.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-pubsub.Channel():
			if !ok {
				return
			}
			hub.Broadcast([]byte(msg.Payload))
		}
	}
}
