package worker

import (
	"context"
	"log/slog"
	"sync"

	paho "github.com/eclipse/paho.mqtt.golang"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	platformmqtt "firefreeforest/backend/internal/platform/mqtt"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
)

type Worker struct {
	cfg      *config.Config
	repo     *repository.Repository
	rdb      *goredis.Client
	mqtt     *platformmqtt.Client
	risk     *service.RiskEngine
	notifier *service.Notifier
	log      *slog.Logger
	ctx      context.Context
}

func New(
	ctx context.Context,
	cfg *config.Config,
	repo *repository.Repository,
	rdb *goredis.Client,
	mqttClient *platformmqtt.Client,
	risk *service.RiskEngine,
	notifier *service.Notifier,
	log *slog.Logger,
) *Worker {
	return &Worker{
		cfg:      cfg,
		repo:     repo,
		rdb:      rdb,
		mqtt:     mqttClient,
		risk:     risk,
		notifier: notifier,
		log:      log,
		ctx:      ctx,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	w.ctx = ctx
	var wg sync.WaitGroup

	wg.Add(3)
	go func() {
		defer wg.Done()
		w.runOutbox(ctx)
	}()
	go func() {
		defer wg.Done()
		w.runSweeper(ctx)
	}()
	go func() {
		defer wg.Done()
		w.runRetention(ctx)
	}()

	<-ctx.Done()
	wg.Wait()
	return nil
}

func (w *Worker) SubscribeAll() {
	qos := byte(w.cfg.MQTTQoS)
	topics := []struct {
		topic   string
		handler paho.MessageHandler
	}{
		{"free-flame-forest/+/telemetry/environment", w.handleEnvironment},
		{"free-flame-forest/+/telemetry/fire", w.handleFire},
		{"free-flame-forest/+/event", w.handleDeviceEvent},
		{"free-flame-forest/+/command/ack", w.handleCommandAck},
	}
	for _, sub := range topics {
		w.subscribe(sub.topic, qos, sub.handler)
	}
}
