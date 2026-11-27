GO ?= go
COMPOSE ?= podman-compose

.PHONY: build test vet tidy migrate run-api run-worker run-mcp run-sim up down logs ps provision mqtt-sync

build:
	$(GO) build ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

tidy:
	GOSUMDB=off GOFLAGS=-mod=mod $(GO) mod tidy

migrate:
	@set -a; [ -f .env ] && . ./.env; set +a; $(GO) run ./cmd/migrate

run-api:
	@set -a; [ -f .env ] && . ./.env; set +a; $(GO) run ./cmd/api

run-worker:
	@set -a; [ -f .env ] && . ./.env; set +a; $(GO) run ./cmd/worker

run-mcp:
	@set -a; [ -f .env ] && . ./.env; set +a; $(GO) run ./cmd/mcp

SIM_ARGS ?= -fire

run-sim:
	@set -a; [ -f .env ] && . ./.env; set +a; \
	$(GO) run ./cmd/simulator \
		-device "$${SEED_DEVICE_ID:-FFF-VIRTUAL-DEVICE}" \
		-broker "$${MQTT_BROKER:-tcp://localhost:1883}" \
		-username "$${MQTT_DEVICE_USERNAME:?set MQTT_DEVICE_USERNAME}" \
		-password "$${MQTT_DEVICE_PASSWORD:?set MQTT_DEVICE_PASSWORD}" \
		-prefix "$${MQTT_TOPIC_PREFIX:-fire-free-forest}" \
		$(SIM_ARGS)

up:
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=100

ps:
	$(COMPOSE) ps

provision: mqtt-sync

mqtt-sync:
	$(COMPOSE) up --force-recreate mosquitto-init
	$(COMPOSE) kill -s HUP mosquitto
