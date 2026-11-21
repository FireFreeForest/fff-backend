package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	gormlogger "gorm.io/gorm/logger"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
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
	keys := service.NewMCPKeyService(repo)
	principal, err := resolvePrincipal(ctx, cfg, repo, keys, log)
	if err != nil {
		return err
	}
	commands := service.NewCommandService(repo, rdb, log, cfg.AutoPumpMax, cfg.MQTTTopicPrefix)
	geocoder := service.NewGeocoder(rdb, cfg.GeocodeBaseURL, cfg.GeocodeUserAgent, cfg.GeocodeCacheTTL, log)

	server := mcptransport.NewServer(mcptransport.Deps{
		Config:   cfg,
		Repo:     repo,
		Commands: commands,
		Geocoder: geocoder,
		Keys:     keys,
		Redis:    rdb,
		Logger:   log,
		Version:  version,
	}, principal.Role)

	log.Info("mcp stdio server starting",
		"role", principal.Role,
		"key_name", principal.KeyName,
		"actor_linked", principal.ActorID != nil,
		"version", version,
	)
	return server.Run(mcptransport.WithPrincipal(ctx, principal), &sdkmcp.StdioTransport{})
}

// resolvePrincipal prefers a database-backed MCP key. When MCP_API_KEY is not
// set, the stdio binary falls back to the explicitly configured MCP_ROLE and
// optional MCP_ACTOR_ID, which is useful for local desktop agents.
func resolvePrincipal(ctx context.Context, cfg *config.Config, repo *repository.Repository, keys *service.MCPKeyService, log *slog.Logger) (*service.MCPPrincipal, error) {
	if token := strings.TrimSpace(cfg.MCPAPIKey); token != "" {
		principal, err := keys.Authenticate(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("MCP_API_KEY tidak valid: %w", err)
		}
		return principal, nil
	}

	var actorID *uuid.UUID
	if cfg.MCPActorID != "" {
		id, err := uuid.Parse(cfg.MCPActorID)
		if err != nil {
			return nil, fmt.Errorf("MCP_ACTOR_ID tidak valid: %w", err)
		}
		user, err := repo.GetUserByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("MCP_ACTOR_ID: %w", err)
		}
		if !user.IsActive {
			return nil, fmt.Errorf("MCP_ACTOR_ID: user tidak aktif")
		}
		if user.Role != cfg.MCPRole {
			return nil, fmt.Errorf("MCP_ACTOR_ID: role user (%s) tidak sama dengan MCP_ROLE (%s)", user.Role, cfg.MCPRole)
		}
		actorID = &id
	} else if cfg.MCPRole == domain.RolePetugas || cfg.MCPRole == domain.RoleAdmin {
		log.Warn("stdio mcp has no linked actor; operational tools will be rejected", "role", cfg.MCPRole)
	}

	return &service.MCPPrincipal{
		KeyName: "stdio-env",
		Role:    cfg.MCPRole,
		ActorID: actorID,
	}, nil
}
