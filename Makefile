GO ?= go
COMPOSE ?= podman-compose

.PHONY: build test vet tidy run-api run-worker run-sim up down logs ps provision

build:
	$(GO) build ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

tidy:
	GOSUMDB=off GOFLAGS=-mod=mod $(GO) mod tidy

run-api:
	@set -a; [ -f .env ] && . ./.env; set +a; $(GO) run ./cmd/api

run-worker:
	@set -a; [ -f .env ] && . ./.env; set +a; $(GO) run ./cmd/worker

SIM_ARGS ?= -fire

run-sim:
	@set -a; [ -f .env ] && . ./.env; set +a; \
	$(GO) run ./cmd/simulator \
		-device "$${SEED_DEVICE_ID:-DEVICE-123}" \
		-broker "$${MQTT_BROKER:-tcp://localhost:1883}" \
		-username "$${MQTT_DEVICE_USERNAME:-DEVICE-123}" \
		-password "$${MQTT_DEVICE_PASSWORD:-device123}" \
		$(SIM_ARGS)

up:
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=100

ps:
	$(COMPOSE) ps

provision:
	./scripts/provision_device.sh "$(DEVICE)" "$(PASSWORD)"
