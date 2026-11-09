# Fire Free Forest - Backend

Backend IoT untuk deteksi dini kebakaran hutan. Perangkat mengirim telemetri melalui MQTT, backend menyimpan data, mengevaluasi risiko, membuat event, mengirim notifikasi FCM, dan menyajikan REST API serta WebSocket realtime untuk dashboard.

## Arsitektur

```
Device (ESP32) --MQTT--> Mosquitto --> Worker --> PostgreSQL
                                          |          ^
                                          v          |
                                        Redis <----- API (REST + WebSocket)
                                          |
                                   FCM Push Notification
```

- `cmd/api` - REST API, WebSocket, autentikasi, dashboard.
- `cmd/worker` - konsumen MQTT, risk engine, outbox dispatcher, sweeper, retensi.
- `cmd/simulator` - simulator perangkat untuk demo tanpa hardware.
- PostgreSQL - sumber data utama (users, devices, telemetri, events, commands, audit).
- Redis - cache telemetri terbaru, level risiko, Pub/Sub realtime, tiket WebSocket, rate limit login.
- Outbox pattern - command disimpan lalu dipublikasikan ke MQTT secara andal oleh worker.

## Menjalankan dengan Podman Compose

Prasyarat: Podman dan `podman-compose` (atau `podman compose`).

```bash
cp .env.example .env
# ubah JWT_SECRET, POSTGRES_PASSWORD, dan kredensial MQTT sebelum production
podman-compose up --build -d
podman-compose ps
```

Perintah `make up`, `make down`, `make logs`, dan `make ps` memakai `podman-compose` secara default. Jika memakai Docker Compose, jalankan `make up COMPOSE="docker compose"`.

Layanan:

| Layanan | Port | Keterangan |
| --- | --- | --- |
| api | 8080 | REST API, health, metrics, WebSocket |
| mosquitto | 1883 | MQTT broker |
| postgres | internal | PostgreSQL 16 |
| redis | internal | Redis 7 |

Migrasi database berjalan otomatis saat startup (`MIGRATE_ON_START=true`).

Cek kesehatan:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

Login admin awal:

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@fff.local","password":"admin12345"}'
```

Ganti password admin default sebelum dipakai di production.

## Menjalankan tanpa container

Prasyarat: Go 1.26+, PostgreSQL 16+, Redis 7+, Mosquitto 2+.

```bash
cp .env.example .env
# sesuaikan DB_DSN, REDIS_ADDR, MQTT_BROKER
make run-api      # terminal 1
make run-worker   # terminal 2
make run-sim      # terminal 3 (simulator dengan flame aktif)
```

Atau jalankan binary hasil build:

```bash
make build
./api
./worker
```

## Simulator Perangkat

```bash
go run ./cmd/simulator \
  -device DEVICE-123 \
  -broker tcp://localhost:1883 \
  -username DEVICE-123 \
  -password device123 \
  -env-interval 30s \
  -fire-interval 5s \
  -fire
```

Flag penting:

- `-fire` mensimulasikan api terdeteksi (memicu event `FIRE_DETECTED` dan auto pump).
- `-smoke`, `-temp`, `-humidity`, `-soil` mengatur nilai dasar sensor.
- Simulator mendengarkan topik command dan mengirim ACK.

## Autentikasi

- `POST /api/v1/auth/login` menghasilkan access token (15 menit) dan refresh token (7 hari).
- Gunakan header `Authorization: Bearer <access_token>`.
- Refresh token dirotasi setiap kali dipakai. Reuse token yang sudah dicabut mencabut seluruh sesi user.
- Role: `admin`, `petugas`, `viewer`.

Contoh:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@fff.local","password":"admin12345"}' | jq -r '.data.tokens.access_token')

curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/devices
```

## Dokumentasi API

Swagger UI interaktif tersedia saat API berjalan:

- `http://localhost:8080/docs` - Swagger UI
- `http://localhost:8080/docs/openapi.yaml` - spec OpenAPI 3.0.3 mentah (bisa diimpor ke Postman/Swagger Editor)

Spec sumber ada di `api/openapi.yaml` dan di-embed ke binary saat build. UI memuat aset Swagger UI dari CDN, jadi browser membutuhkan akses internet.

## Endpoint REST

Semua endpoint di bawah prefix `/api/v1`.

| Method | Path | Role | Fungsi |
| --- | --- | --- | --- |
| POST | `/auth/login` | publik | Login |
| POST | `/auth/refresh` | publik | Rotasi token |
| GET | `/auth/me` | semua | Profil user |
| POST | `/auth/logout` | semua | Cabut refresh token |
| GET | `/users` | admin | Daftar user |
| POST | `/users` | admin | Tambah user |
| PATCH | `/users/:userID` | admin | Ubah user |
| GET | `/devices` | semua | Daftar device |
| POST | `/devices` | admin | Tambah device |
| GET | `/devices/:deviceID` | semua | Detail device |
| PATCH | `/devices/:deviceID` | admin | Ubah device |
| DELETE | `/devices/:deviceID` | admin | Hapus device |
| GET | `/devices/:deviceID/status` | semua | Status + telemetri terbaru |
| GET | `/devices/:deviceID/telemetry/latest` | semua | Telemetri terbaru |
| GET | `/devices/:deviceID/telemetry/environment` | semua | Riwayat lingkungan |
| GET | `/devices/:deviceID/telemetry/fire` | semua | Riwayat api/asap |
| GET | `/devices/:deviceID/commands` | semua | Riwayat command |
| POST | `/devices/:deviceID/commands` | admin, petugas | Kirim command pompa |
| GET | `/commands/:commandID` | semua | Detail command |
| GET | `/events` | semua | Daftar event |
| GET | `/events/:eventID` | semua | Detail event |
| POST | `/events/:eventID/acknowledge` | admin, petugas | Tandai event ditangani |
| POST | `/events/:eventID/resolve` | admin, petugas | Tutup event |
| GET | `/dashboard/summary` | semua | Ringkasan dashboard |
| POST | `/push/tokens` | semua | Daftarkan token FCM |
| DELETE | `/push/tokens/:token` | semua | Hapus token FCM |
| POST | `/realtime/ticket` | semua | Tiket WebSocket (60 detik) |
| GET | `/ws?ticket=...` | tiket | Koneksi WebSocket |

Filter daftar event: `severity`, `status`, `event_type`, `device_id`, `from`, `to` (RFC3339), `page`, `per_page`.

Command pompa:

```bash
curl -X POST http://localhost:8080/api/v1/devices/DEVICE-123/commands \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: pump-001' \
  -d '{"action":"SET_PUMP","payload":{"state":true,"duration":30}}'
```

## WebSocket

1. `POST /api/v1/realtime/ticket` dengan Bearer token.
2. Buka `ws://localhost:8080/api/v1/ws?ticket=<ticket>&device_id=DEVICE-123` (filter opsional).
3. Pesan berupa JSON envelope:

```json
{
  "type": "telemetry.fire",
  "device_id": "DEVICE-123",
  "data": {"telemetry": {}, "device_id": "DEVICE-123"},
  "timestamp": "2026-01-01T10:00:00Z"
}
```

Tipe: `connected`, `telemetry.environment`, `telemetry.fire`, `event.created`, `event.updated`, `event.resolved`, `risk.updated`, `command.updated`.

## Kontrak MQTT

Prefix sesuai dokumen proyek: `free-flame-forest`.

| Topik | Arah | QoS |
| --- | --- | --- |
| `free-flame-forest/{device_id}/telemetry/environment` | device -> backend | 1 |
| `free-flame-forest/{device_id}/telemetry/fire` | device -> backend | 1 |
| `free-flame-forest/{device_id}/event` | device -> backend | 1 |
| `free-flame-forest/{device_id}/command` | backend -> device | 1 |
| `free-flame-forest/{device_id}/command/ack` | device -> backend | 1 |

Payload harus JSON valid (tanpa trailing comma).

Telemetri lingkungan:

```json
{
  "device_id": "DEVICE-123",
  "sensors": {"temperature": 31.5, "humidity": 62.0, "soil_moisture": 55.0},
  "timestamp": "2026-01-01T10:00:00Z"
}
```

Telemetri api/asap:

```json
{
  "device_id": "DEVICE-123",
  "sensors": {"flame_detected": false, "smoke_level": 120},
  "actuators": {"pump": false, "buzzer": false},
  "timestamp": "2026-01-01T10:00:00Z"
}
```

Event dari perangkat:

```json
{
  "device_id": "DEVICE-123",
  "event_type": "SENSOR_ERROR",
  "severity": "error",
  "source": "flame_sensor",
  "message": "Sensor api tidak merespons",
  "timestamp": "2026-01-01T10:00:00Z"
}
```

Command dari backend:

```json
{
  "device_id": "DEVICE-123",
  "command_id": "cmd-20260101-AB12",
  "action": "SET_PUMP",
  "payload": {"state": true, "duration": 30},
  "timestamp": "2026-01-01T10:00:00Z"
}
```

ACK perangkat:

```json
{
  "device_id": "DEVICE-123",
  "command_id": "cmd-20260101-AB12",
  "status": "success",
  "message": "Pump activated"
}
```

Catatan: `event_id` pada payload `/event` yang tidak kosong diabaikan untuk mencegah loop event yang dibuat backend.

## Aturan Risiko

| Kondisi | Event | Severity |
| --- | --- | --- |
| `flame_detected=true` | `FIRE_DETECTED` | critical |
| `smoke_level > THRESHOLD_SMOKE_LEVEL` | `SMOKE_DETECTED` | warning |
| `temperature > THRESHOLD_TEMPERATURE_HIGH_C` | `TEMPERATURE_HIGH` | warning |
| `soil_moisture < THRESHOLD_SOIL_DRY_PCT` | `SOIL_DRY` | warning |
| Perangkat tidak mengirim data > `DEVICE_OFFLINE_AFTER` | `DEVICE_OFFLINE` | warning |
| Payload/sensor tidak valid | `SENSOR_ERROR` | error |

Level risiko: `NORMAL`, `WASPADA` (1 warning), `BAHAYA` (>=2 warning), `DARURAT` (critical).

Event aktif otomatis resolved bila kondisi sudah normal kembali. Cooldown mencegah duplikasi event. Saat `FIRE_DETECTED`, auto pump mengirim command `SET_PUMP` bila `AUTO_PUMP_ENABLED=true`.

## Provisioning Device MQTT

Device default `DEVICE-123` dibuat otomatis saat pertama kali inisialisasi volume Mosquitto. Untuk device baru:

```bash
make provision DEVICE=DEVICE-456 PASSWORD=device456
```

Script menambahkan user MQTT dan mengirim SIGHUP ke broker. ACL memakai pola username sehingga device hanya dapat menulis ke topik miliknya sendiri. Device juga otomatis terdaftar di database saat pesan pertama diterima (`AUTO_REGISTER_DEVICES=true`).

## Environment Variables

Lihat `.env.example`. Variabel wajib:

- `DB_DSN`
- `JWT_SECRET` minimal 24 karakter

Variabel penting lain: `REDIS_ADDR`, `MQTT_BROKER`, `MQTT_USERNAME`, `MQTT_PASSWORD`, threshold risiko, auto pump, `FCM_CREDENTIALS_FILE`, `CORS_ORIGINS`, `WS_ALLOWED_ORIGIN`.

Untuk mengaktifkan FCM:

1. Letakkan service account JSON di `deploy/fcm/service-account.json`.
2. Set `FCM_CREDENTIALS_FILE=/app/secrets/service-account.json` di `.env`.
3. Set `FCM_PROJECT_ID` bila project ID tidak ada di file service account.
4. Jalankan `podman-compose up -d worker` (file di-mount read-only ke worker).

Untuk menjalankan native, set `FCM_CREDENTIALS_FILE` ke path absolut file tersebut.

### Menyiapkan Firebase/FCM dari awal

FCM hanya dipakai untuk push notification; data utama tetap disimpan di PostgreSQL.

1. Buka [Firebase Console](https://console.firebase.google.com/), buat project, lalu tambahkan aplikasi Android (isi package name aplikasi pemantau) atau aplikasi web.
2. Unduh konfigurasi client (`google-services.json` untuk Android atau konfigurasi Firebase web) dan pasang di aplikasi client. File ini bukan untuk backend.
3. Di Firebase Console buka **Project settings > Service accounts**, pilih **Generate new private key**, lalu simpan file JSON ke `deploy/fcm/service-account.json` dan jalankan `chmod 644 deploy/fcm/service-account.json`.
4. Pastikan **Firebase Cloud Messaging API** aktif di Google Cloud Console untuk project tersebut.
5. Isi `.env`:

```bash
FCM_CREDENTIALS_FILE=/app/secrets/service-account.json
FCM_PROJECT_ID=<project-id>
FCM_MIN_SEVERITY=critical
```

6. Jalankan `podman-compose up -d worker`, lalu cek `podman-compose logs worker`. Log harus berisi `fcm notifications enabled`; jika masih `disabled`, periksa path dan isi file JSON.
7. Dapatkan token FCM dari aplikasi client:
   - Android: `FirebaseMessaging.getInstance().getToken()`.
   - Web: `getToken(messaging, { vapidKey: "..." })`.
   Token ini bukan access token JWT dan hanya bisa diperoleh dari aplikasi yang sudah terhubung ke project Firebase.
8. Login sebagai admin, daftarkan token, lalu picu event api:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@fff.local","password":"admin12345"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["tokens"]["access_token"])')

curl -X POST http://localhost:8080/api/v1/push/tokens \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"token":"<FCM_DEVICE_TOKEN>","platform":"android"}'
```

9. Jalankan simulator dengan `-fire` atau kirim telemetri api; worker akan mengirim notifikasi ke token admin/petugas yang terdaftar.

Tanpa service account dan token dari aplikasi client, integrasi FCM hanya bisa diverifikasi sampai worker menyala; pengiriman ke perangkat tidak bisa diuji.

## Observability

- `GET /healthz` - liveness.
- `GET /readyz` - kesiapan PostgreSQL/Redis.
- `GET /metrics` - metrik Prometheus (HTTP, MQTT ingest, event, command, FCM).
- Log terstruktur (`slog`), format JSON saat `APP_ENV=production`.
- Audit log untuk login, perubahan device, command, dan aksi event.

## Testing dan Build

```bash
make build
make vet
make test
```

Jika `go mod tidy` gagal karena TLS ke `sum.golang.org`, gunakan:

```bash
GOSUMDB=off GOFLAGS=-mod=mod go mod tidy
```

Integration test API membutuhkan PostgreSQL khusus (test akan mengosongkan tabel):

```bash
TEST_DB_DSN='host=localhost user=fff password=fff dbname=fff_test port=5432 sslmode=disable' \
  go test -tags=integration ./internal/transport/http -run TestAPIFlowIntegration -v
```

## Struktur Proyek

```
backend/
  cmd/api/            entrypoint REST API
  cmd/worker/         entrypoint worker MQTT
  cmd/simulator/      simulator perangkat
  internal/config/    konfigurasi env
  internal/domain/    model dan enum
  internal/platform/  postgres, redis, mqtt, fcm, logger
  internal/repository query database
  internal/service/   auth, command, risk engine, notifier
  internal/worker/    consumer, outbox, sweeper
  internal/transport/ HTTP handler + WebSocket hub
  migrations/         migrasi SQL
  deploy/mosquitto/   konfigurasi broker dan provisioning
  scripts/            provisioning device
```

## Keamanan Produksi

- Ganti semua kredensial default (`JWT_SECRET`, password admin, password MQTT, password PostgreSQL).
- Set `CORS_ORIGINS` dan `WS_ALLOWED_ORIGIN` ke domain dashboard, jangan `*`.
- Gunakan TLS di reverse proxy untuk API dan WebSocket.
- Batasi akses port MQTT atau gunakan TLS listener.
- Simpan service account FCM sebagai secret, jangan commit ke repository.
- Backup volume PostgreSQL secara berkala.
