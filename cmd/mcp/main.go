package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	gormlogger "gorm.io/gorm/logger"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/platform/logger"
	"firefreeforest/backend/internal/platform/postgres"
	platformredis "firefreeforest/backend/internal/platform/redis"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
	mcptransport "firefreeforest/backend/internal/transport/mcp"
)

var version = "1.0.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadMCP()
	if err != nil {
		return err
	}
	log := logger.NewWithWriter(cfg.LogLevel, "text", os.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.OpenWithLogger(cfg.DBDSN, gormlogger.Default.LogMode(gormlogger.Silent))
	if err != nil {
		return err
	}
	rdb, err := platformredis.Open(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer rdb.Close()

	repo := repository.New(db)
	geocoder := service.NewGeocoder(rdb, cfg.GeocodeBaseURL, cfg.GeocodeUserAgent, cfg.GeocodeCacheTTL, log)

	server := mcptransport.NewServer(mcptransport.Deps{
		Config:   cfg,
		Repo:     repo,
		Geocoder: geocoder,
		Redis:    rdb,
		Logger:   log,
		Version:  version,
	})

	log.Info("mcp stdio server starting (read-only)", "version", version)
	return server.Run(ctx, &sdkmcp.StdioTransport{})
}
