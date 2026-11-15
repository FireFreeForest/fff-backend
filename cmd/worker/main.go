package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/platform/fcm"
	"firefreeforest/backend/internal/platform/logger"
	platformmqtt "firefreeforest/backend/internal/platform/mqtt"
	"firefreeforest/backend/internal/platform/postgres"
	platformredis "firefreeforest/backend/internal/platform/redis"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
	"firefreeforest/backend/internal/worker"
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
	commands := service.NewCommandService(repo, rdb, log, cfg.AutoPumpMax, cfg.MQTTTopicPrefix)
	riskEngine := service.NewRiskEngine(repo, rdb, commands, cfg, log)

	fcmClient, err := fcm.New(ctx, cfg.FCMCredentialsFile, cfg.FCMProjectID, log)
	if err != nil {
		return err
	}
	if fcmClient.Enabled() {
		log.Info("fcm notifications enabled",
			"min_severity", cfg.FCMMinSeverity,
			"event_types", strings.Join(cfg.FCMEventTypes, ","),
		)
	} else {
		log.Warn("fcm notifications disabled; set FCM_CREDENTIALS_FILE to enable")
	}
	notifier := service.NewNotifier(fcmClient, repo, domain.Severity(cfg.FCMMinSeverity), cfg.FCMEventTypes, log)

	clientID := cfg.MQTTClientID
	if clientID == "" {
		clientID = "fff-worker-" + uuid.NewString()[:8]
	}
	mqttClient := platformmqtt.New(platformmqtt.Options{
		Broker:   cfg.MQTTBroker,
		ClientID: clientID,
		Username: cfg.MQTTUsername,
		Password: cfg.MQTTPassword,
	}, log)

	w := worker.New(ctx, cfg, repo, rdb, mqttClient, riskEngine, notifier, log)
	mqttClient.SetOnConnect(w.SubscribeAll)

	if err := mqttClient.Connect(ctx); err != nil {
		return fmt.Errorf("connect mqtt: %w", err)
	}
	defer mqttClient.Disconnect()

	log.Info("worker started", "mqtt_broker", cfg.MQTTBroker)
	return w.Run(ctx)
}
