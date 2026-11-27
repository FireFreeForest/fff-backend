<p align="center">
  <img src="docs/logo.png" alt="Logo Fire Free Forest" width="160">
</p>

# Fire Free Forest - Backend

Backend IoT untuk deteksi dini kebakaran hutan. Perangkat mengirim telemetri melalui MQTT, backend menyimpan data, mengevaluasi risiko, membuat event, mengirim notifikasi FCM, dan menyajikan REST API serta WebSocket realtime untuk dashboard.

## Arsitektur

```
Device (ESP32) atau Website Virtual Device --MQTT--> Mosquitto --> Worker --> PostgreSQL
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
| mosquitto | 1883 | MQTT broker (TCP, device) |
| mosquitto | 9001 | MQTT over WebSocket (website virtual device) |
| postgres | internal | PostgreSQL 16 |
| redis | internal | Redis 7 |

Migrasi database dijalankan sebagai langkah terpisah oleh service satu kali `migrate` sebelum `api` dan `worker` start. Service aplikasi memakai `MIGRATE_ON_START=false` agar tidak ada DDL saat worker sudah berjalan. Untuk menjalankan migrasi manual:

```bash
make migrate          # memuat .env lalu go run ./cmd/migrate
go run ./cmd/migrate  # tanpa Makefile
./bin/migrate         # binary hasil build
```

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
make migrate      # terapkan migrasi dulu
make run-api      # terminal 1
make run-worker   # terminal 2
make run-sim      # terminal 3 (simulator dengan flame aktif)
```

Atau jalankan binary hasil build:

```bash
go build -o migrate ./cmd/migrate
go build -o api ./cmd/api
go build -o worker ./cmd/worker
./migrate
./api
./worker
```

## Simulator Perangkat

Cara termudah, kredensial dibaca dari `.env`:

```bash
make run-sim
```

Atau secara manual:

```bash
set -a; . ./.env; set +a
go run ./cmd/simulator \
  -device FFF-VIRTUAL-DEVICE \
  -broker tcp://localhost:1883 \
  -username "$MQTT_DEVICE_USERNAME" \
  -password "$MQTT_DEVICE_PASSWORD" \
  -prefix fire-free-forest \
  -env-interval 30s \
  -fire-interval 5s \
  -fire
```

Flag penting:

- `-fire` mensimulasikan api terdeteksi (memicu event `FIRE_DETECTED`).
- `-smoke`, `-temp`, `-humidity`, `-soil` mengatur nilai dasar sensor; smoke dan soil memakai ADC `0–1023`.
- `-prefix` mengikuti `MQTT_TOPIC_PREFIX` (default `fire-free-forest`).
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
| GET | `/users` | admin | Daftar user; `q` mencari nama/email |
| POST | `/users` | admin | Tambah user |
| PATCH | `/users/:userID` | admin | Ubah user |
| GET | `/devices` | semua | Daftar device; `q` mencari nama/lokasi/device ID |
| POST | `/devices` | admin | Tambah device; `device_id` opsional dan dibuat otomatis bila kosong |
| GET | `/devices/:deviceID` | semua | Detail device |
| PATCH | `/devices/:deviceID` | admin | Ubah device |
| DELETE | `/devices/:deviceID` | admin | Hapus device |
| GET | `/devices/:deviceID/status` | semua | Status + telemetri terbaru |
| GET | `/devices/:deviceID/telemetry/latest` | semua | Telemetri terbaru |
| GET | `/devices/:deviceID/telemetry/environment` | semua | Riwayat lingkungan |
| GET | `/devices/:deviceID/telemetry/environment/aggregate` | semua | Rata-rata atau raw lingkungan per interval |
| GET | `/devices/:deviceID/telemetry/fire` | semua | Riwayat api/asap |
| GET | `/devices/:deviceID/telemetry/fire/aggregate` | semua | Rata-rata atau raw api/asap per interval |
| GET | `/devices/:deviceID/telemetry/device` | semua | Riwayat telemetri perangkat (firmware, WiFi, baterai) |
| GET | `/devices/:deviceID/commands` | semua | Riwayat command |
| POST | `/devices/:deviceID/commands` | admin, petugas | Kirim command (`SET_PUMP` atau `GET_ALL_TELEMETRY`) |
| GET | `/commands/:commandID` | semua | Detail command |
| GET | `/events` | semua | Daftar event |
| GET | `/events/:eventID` | semua | Detail event + preview linimasa |
| GET | `/events/:eventID/timeline` | semua | Linimasa lengkap event |
| POST | `/events/:eventID/acknowledge` | admin, petugas | Tandai event ditangani |
| POST | `/events/:eventID/resolve` | petugas | Tutup event |
| GET | `/dashboard/summary` | semua | Ringkasan dashboard |
| GET | `/geocode/reverse?lat=&lon=` | semua | Label lokasi dari koordinat (proxy Nominatim + cache Redis) |
| POST | `/push/tokens` | semua | Daftarkan token FCM |
| DELETE | `/push/tokens/:token` | semua | Hapus token FCM |
| POST | `/realtime/ticket` | semua | Tiket WebSocket (60 detik) |
| GET | `/ws?ticket=...` | tiket | Koneksi WebSocket |

Filter daftar event: `severity`, `status`, `event_type`, `device_id`, `from`, `to` (RFC3339), `page`, `per_page`.

Setiap response event menyertakan objek `device` lengkap, sehingga klien tidak perlu lookup device terpisah. Field `device_id` pada event adalah UUID internal; kode perangkat (mis. `FFF-A1B2C3D4`) ada di `device.device_id`.

### Linimasa event

`GET /events/:eventID` mengembalikan `resolved_by`, `timeline_preview` berisi maksimal 5 entri terakhir, `timeline_total`, dan `timeline_has_more`. Untuk riwayat lengkap gunakan `GET /events/:eventID/timeline?page=1&per_page=50&include=all`; `include=important` menyembunyikan notifikasi yang dilewati.

Setiap entri mencatat `kind`, `title`, `description`, `outcome`, `level`, `actor`, `metadata`, dan `occurred_at`. Aktor `system` dipakai untuk deteksi, notifikasi, reminder, eskalasi, dan auto-resolve; aktor `user` dipakai untuk acknowledge dan resolve. Semua percobaan notifikasi dicatat sebagai `sent`, `partial`, `failed`, atau `skipped` beserta alasannya. Event lama di-backfill untuk entri created/acknowledged/resolved serta reminder/escalation yang timestamp-nya masih tersedia.

Saat `device_id` tidak dikirim pada `POST /devices`, backend membuat ID `FFF-XXXXXXXX` (8 karakter acak) dan mengembalikannya di field `device_id`. Mobile memakai ID dari respons tersebut untuk provisioning SoftAP. ID manual tetap diterima selama cocok dengan pola `A-Za-z0-9._-` sepanjang 3–64 karakter.

Pencarian daftar memakai query `q` yang case-insensitive dan cocok sebagai substring. Device mencari `name`, `location_name`, atau `device_id`; user mencari `name` atau `email`. `q` di-trim dan maksimal 100 karakter; kosong berarti tanpa filter. `meta.total` mengikuti hasil filter.

```bash
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices?q=hutan&page=1&per_page=20"
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices?q=FFF-VIRTUAL-DEVICE"
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/users?q=petugas&page=1&per_page=20"
```

### Agregat dan raw telemetri

Endpoint agregat mengelompokkan data ke bucket interval menit dan menghitung rata-rata. Parameter `from` dan `to` wajib RFC3339, `interval` dalam menit (default `5`, maksimal `1440`). Rentang maksimal 90 hari dan maksimal 5000 bucket; bucket tanpa data tidak dikembalikan. Bucket disejajarkan ke WIB (`Asia/Jakarta`), tetapi `bucket_start`, `from`, dan `to` dikirim dalam UTC.

`interval=0` mengaktifkan mode raw tanpa rata-rata. Rentang raw maksimal 7 hari. Jika sumber data ≤5000 baris, semua baris dikembalikan dengan `mode: "raw"`. Jika lebih dari 5000, backend melakukan decimation yang mempertahankan nilai ekstrem (bukan rata-rata) dan mengembalikan maksimal 5000 titik dengan `mode: "raw_decimated"`. Decimation api/asap memprioritaskan baris `flame_detected=true`; decimation lingkungan mempertahankan min/max temperature, humidity, dan soil moisture. Batas keras sumber raw adalah 250.000 baris; di atas itu response `422 raw_source_too_large`.

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/v1/devices/FFF-VIRTUAL-DEVICE/telemetry/environment/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-01T06:00:00Z&interval=5"

curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/v1/devices/FFF-VIRTUAL-DEVICE/telemetry/fire/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-01T06:00:00Z&interval=0"
```

Response lingkungan berisi `temperature`, `humidity`, `soil_moisture`, dan `sample_count`. Response api/asap berisi `flame_level`, `smoke_level`, `flame_detected_ratio`, `pump_on_ratio`, `buzzer_on_ratio`, dan `sample_count`. Ratio bernilai `0..1`. `soil_moisture` dan `smoke_level` tetap rata-rata ADC mentah `0–1023`, bukan persen. Response raw menambahkan `mode`, `decimated`, `source_points`, dan `returned_points`; setiap titik raw memakai field telemetri asli seperti `recorded_at`, `received_at`, dan `id`.

### Uptime device

Device memiliki `online_since` (waktu mulai online) dan `uptime_seconds` (durasi sejak `online_since`). Saat device terdeteksi offline oleh sweeper (`DEVICE_OFFLINE_AFTER`, default 30 detik), `online_since` dikosongkan dan `uptime_seconds` menjadi `0`. Saat device mengirim data lagi setelah offline, `online_since` diisi waktu reconnect sehingga uptime mereset. Reconnect dalam batas offline tidak mereset uptime. Field ini tersedia di list/detail/status device dan event WebSocket `device.status`.

`GET /geocode/reverse` memakai Nominatim/OpenStreetMap, menyimpan hasil di Redis (`GEOCODE_CACHE_TTL`, default `720h`), dan membatasi request ke Nominatim maksimal 1 request/detik. Set `GEOCODE_USER_AGENT` ke identitas aplikasi yang valid untuk penggunaan produksi.

Command pompa:

```bash
curl -X POST http://localhost:8080/api/v1/devices/FFF-VIRTUAL-DEVICE/commands \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: pump-001' \
  -d '{"action":"SET_PUMP","payload":{"state":true,"duration":30}}'
```

## MCP (Model Context Protocol)

Backend menyediakan MCP **read-only** untuk agen AI melalui dua transport. MCP tidak dapat mengubah data: tidak ada tool untuk acknowledge/resolve event atau mengirim command.

| Transport | Cara akses | Autentikasi |
| --- | --- | --- |
| Streamable HTTP | `POST /mcp` | header `Authorization: Bearer <MCP_API_KEY>` |
| stdio | binary `./mcp` | tanpa token (proses lokal, read-only) |

Endpoint HTTP `/mcp` aktif otomatis bila `MCP_API_KEY` diisi; tidak ada variabel `MCP_ENABLED`. Tidak ada pembuatan key lewat API/CLI: token hanya disimpan di environment. Untuk merotasi token, ganti `MCP_API_KEY` lalu restart proses. Endpoint HTTP memakai mode stateless dan mengembalikan JSON, sehingga cocok di belakang reverse proxy/Caddy yang sudah meneruskan seluruh trafik ke `api:8080`.

### Token

Isi `MCP_API_KEY` dengan string acak yang panjang, misalnya:

```bash
openssl rand -base64 48
```

Simpan nilainya hanya di secret manager atau `.env`, dan kirim sebagai Bearer token:

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Authorization: Bearer $MCP_API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}'
```

### Tool

Semua tool bersifat baca-saja:

- `list_devices`, `get_device`, `get_device_status`, `get_latest_telemetry`
- `list_environment_telemetry`, `aggregate_environment_telemetry`
- `list_fire_telemetry`, `aggregate_fire_telemetry`
- `list_device_telemetry`
- `list_events`, `get_event`, `list_event_timeline`
- `list_commands`, `get_command`
- `get_dashboard_summary`, `reverse_geocode`

Tidak ada tool tulis. Tindakan operasional (acknowledge/resolve event, kirim command pompa) hanya tersedia lewat REST dengan JWT user.

### Resources dan prompts

Resource JSON:

- `firefreeforest://devices`
- `firefreeforest://events/active`
- `firefreeforest://dashboard/summary`
- `firefreeforest://devices/{device_id}/latest` (template)

Prompt: `triage_events`, `device_health`, dan `shift_briefing`.

### Menjalankan stdio

```bash
go run ./cmd/mcp
```

Binary stdio memakai `DB_DSN` dan `REDIS_ADDR` yang sama dengan API, menulis log ke stderr, tidak membutuhkan `JWT_SECRET`, dan selalu read-only. Contoh konfigurasi klien desktop:

```json
{
  "mcpServers": {
    "fire-free-forest": {
      "command": "/app/mcp",
      "env": {
        "DB_DSN": "host=... user=... password=... dbname=fff",
        "REDIS_ADDR": "redis:6379"
      }
    }
  }
}
```

## WebSocket

1. `POST /api/v1/realtime/ticket` dengan Bearer token.
2. Buka `ws://localhost:8080/api/v1/ws?ticket=<ticket>&device_id=FFF-VIRTUAL-DEVICE` (filter opsional).
3. Pesan berupa JSON envelope:

```json
{
  "type": "telemetry.fire",
  "device_id": "FFF-VIRTUAL-DEVICE",
  "data": {"telemetry": {}, "device_id": "FFF-VIRTUAL-DEVICE"},
  "ts": "2026-01-01T10:00:00Z"
}
```

Tipe: `connected`, `telemetry.environment`, `telemetry.fire`, `telemetry.device`, `event.created`, `event.resolved`, `risk.updated`, `device.status`, `command.issued`, `command.ack`, `command.timeout`.

Payload `event.created`, `event.resolved`, dan `event.updated` menyertakan `data.event.device` berisi data device lengkap, sama seperti response REST event.

Aplikasi Flutter memakai satu koneksi WebSocket global setelah login. Node dan detail node diperbarui langsung dari event; Analitik dan Riwayat memakai refresh debounce. Setelah reconnect, aplikasi meminta ulang tiket dan memuat ulang data untuk mengejar event yang terlewat.

## Kontrak MQTT

Prefix default mengikuti website virtual device: `fire-free-forest`. Ubah lewat `MQTT_TOPIC_PREFIX` bila firmware memakai prefix lain.

| Topik | Arah | QoS |
| --- | --- | --- |
| `fire-free-forest/{device_id}/telemetry/environment` | device -> backend | 1 |
| `fire-free-forest/{device_id}/telemetry/fire` | device -> backend | 1 |
| `fire-free-forest/{device_id}/telemetry/device` | device -> backend | 1 |
| `fire-free-forest/{device_id}/event` | device -> backend | 1 |
| `fire-free-forest/{device_id}/command` | backend -> device | 1 |
| `fire-free-forest/{device_id}/command/ack` | device -> backend | 1 |

Payload harus JSON valid (tanpa trailing comma). Website memublikasikan QoS `0`; subscription backend QoS `1` tetap kompatibel.

Telemetri lingkungan:

```json
{
  "device_id": "FFF-VIRTUAL-DEVICE",
  "sensors": {"temperature": 28.0, "humidity": 65.0, "soil_moisture": 640},
  "timestamp": "2026-01-01T10:00:00Z"
}
```

`temperature` dan `humidity` boleh dihilangkan saat sensor DHT11 error; `soil_moisture` tetap wajib dan memakai ADC mentah `0–1023`.

Telemetri api/asap:

```json
{
  "device_id": "FFF-VIRTUAL-DEVICE",
  "sensors": {
    "flame_detected": false,
    "flame_level": 900,
    "smoke_level": 180
  },
  "actuators": {"pump": false, "buzzer": false},
  "timestamp": "2026-01-01T10:00:00Z"
}
```

Telemetri perangkat:

```json
{
  "device_id": "FFF-VIRTUAL-DEVICE",
  "firmware_version": "1.3.0",
  "wifi": {"rssi": -49},
  "battery": {"voltage": 3.456729, "percentage": 18},
  "timestamp": "2026-01-01T10:00:00Z"
}
```

`firmware_version` yang diterima ikut memperbarui `devices.firmware_version`. Nilai RSSI, tegangan, dan persentase baterai divalidasi (RSSI `-120..0` dBm, tegangan `0..6` V, persentase `0..100`); di luar rentang dianggap `SENSOR_ERROR`. Baterai di bawah `THRESHOLD_BATTERY_LOW_PERCENT` (default `20`, isi `0` untuk menonaktifkan) memicu event `LOW_BATTERY` yang otomatis resolved saat baterai kembali normal.

Event dari perangkat:

```json
{
  "device_id": "FFF-VIRTUAL-DEVICE",
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
  "device_id": "FFF-VIRTUAL-DEVICE",
  "command_id": "cmd-20260101-AB12",
  "action": "SET_PUMP",
  "payload": {"state": true, "duration": 30},
  "timestamp": "2026-01-01T10:00:00Z"
}
```

Selain `SET_PUMP`, backend mendukung action `GET_ALL_TELEMETRY` dengan `payload` kosong. Perangkat membalas `command/ack` setelah mempublikasikan ulang seluruh telemetri (environment, fire, device); ack inilah yang dipakai backend sebagai penanda telemetri terbaru sudah masuk.

ACK perangkat:

```json
{
  "device_id": "FFF-VIRTUAL-DEVICE",
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
| `smoke_level >= THRESHOLD_SMOKE_LEVEL` | `SMOKE_DETECTED` | warning |
| `temperature >= THRESHOLD_TEMPERATURE_HIGH_C` | `TEMPERATURE_HIGH` | warning |
| `THRESHOLD_SOIL_DRY_ADC > 0` dan `soil_moisture < THRESHOLD_SOIL_DRY_ADC` | `SOIL_DRY` | warning |
| `THRESHOLD_BATTERY_LOW_PERCENT > 0` dan `battery.percentage < THRESHOLD_BATTERY_LOW_PERCENT` | `LOW_BATTERY` | warning |
| Perangkat tidak mengirim data > `DEVICE_OFFLINE_AFTER` | `DEVICE_OFFLINE` | warning |
| Payload/sensor tidak valid | `SENSOR_ERROR` | error |

Level risiko: `NORMAL`, `WASPADA` (1 warning), `BAHAYA` (>=2 warning), `DARURAT` (critical).

Event aktif otomatis resolved bila kondisi sudah normal kembali. Cooldown mencegah duplikasi event.

Setiap event menyertakan `metadata` berisi snapshot telemetri terbaru saat event dibuat: `temperature`, `humidity`, `soil_moisture`, `flame_detected`, `flame_level`, `smoke_level`, `pump`, `firmware_version`, `wifi_rssi`, `battery_voltage`, dan `battery_percentage`. Sebelum membuat event, backend mengirim `GET_ALL_TELEMETRY` dan menunggu ack perangkat (maksimal `TELEMETRY_REFRESH_TIMEOUT`, default 5 detik) agar snapshot benar-benar segar; bila ack tidak datang, metadata memakai snapshot cache terakhir. Field `metadata_source` menandai sumbernya (`command` atau `cache`) dan `command_id` menyimpan id command refresh. Refresh dibatasi per device (`TELEMETRY_REFRESH_THROTTLE`, default 30 detik) agar tidak membanjiri perangkat; `DEVICE_OFFLINE` selalu memakai snapshot cache karena perangkat tidak dapat menjawab.

Auto pump backend **nonaktif secara default** karena website/perangkat sudah memiliki logika auto pump. Bila `AUTO_PUMP_ENABLED=true`, backend mengirim `SET_PUMP` sebagai fallback saat api terdeteksi atau `smoke_level >= THRESHOLD_SMOKE_LEVEL`. `SOIL_DRY` aktif secara default (`THRESHOLD_SOIL_DRY_ADC=300`); sesuaikan nilai tersebut dengan hasil kalibrasi sensor tanah.

## Provisioning Device MQTT

Semua device memakai **satu akun MQTT bersama**. Identitas device diambil dari MQTT client ID, dan client ID **wajib sama dengan `device_id`**. ACL memakai pola `%c` sehingga setiap device hanya bisa menulis ke topik miliknya sendiri.

1. Isi `.env` dengan kredensial acak:
   ```bash
   MQTT_DEVICE_USERNAME=fff-<random>
   MQTT_DEVICE_PASSWORD=<random-panjang>
   ```
2. Sinkronkan akun ke Mosquitto:
   ```bash
   make mqtt-sync
   # alias lama: make provision
   ```
3. Di firmware/website, set username dan password yang sama, lalu set client ID = `device_id`.

Tidak ada lagi pembuatan user per device. Script `scripts/provision_device.sh` sekarang hanya menyinkronkan akun bersama dan mengirim SIGHUP ke broker. Device otomatis terdaftar di database saat pesan pertama diterima (`AUTO_REGISTER_DEVICES=true`).

Catatan migrasi: user per-device lama pada volume Mosquitto yang sudah ada tidak dihapus otomatis. Untuk produksi, gunakan volume baru atau hapus user lama setelah semua device pindah ke akun bersama.

### Integrasi Website Virtual Device

Website acuan: `https://virtual-device-six.vercel.app/`.

1. Pastikan akun bersama sudah disinkronkan dengan `make mqtt-sync`.
2. Di pengaturan website, isi broker `ws://localhost:9001/mqtt` untuk backend lokal (atau `ws://<host>:9001/mqtt`).
3. Isi prefix `fire-free-forest`, device ID `FFF-VIRTUAL-DEVICE`, username dan password dari `.env`, serta **client ID = `FFF-VIRTUAL-DEVICE`**.
4. Untuk deployment publik, terminasi TLS di reverse proxy dan gunakan `wss://`.
5. Website tetap menjadi simulator acuan; backend tidak menggantikan simulator perangkat.

Catatan: port `9001` adalah MQTT-over-WebSocket untuk perangkat/website. WebSocket realtime aplikasi tetap berada di `ws://localhost:8080/api/v1/ws`.

## Environment Variables

Lihat `.env.example`. Variabel wajib:

- `DB_DSN`
- `JWT_SECRET` minimal 24 karakter

Variabel penting lain: `REDIS_ADDR`, `MQTT_BROKER`, `MQTT_USERNAME`, `MQTT_PASSWORD`, `MQTT_DEVICE_USERNAME`, `MQTT_DEVICE_PASSWORD`, `MQTT_TOPIC_PREFIX`, `THRESHOLD_SMOKE_LEVEL`, `THRESHOLD_TEMPERATURE_HIGH_C`, `THRESHOLD_SOIL_DRY_ADC`, `THRESHOLD_BATTERY_LOW_PERCENT`, `TELEMETRY_REFRESH_TIMEOUT`, `TELEMETRY_REFRESH_THROTTLE`, `AUTO_PUMP_ENABLED`, `GEOCODE_BASE_URL`, `GEOCODE_USER_AGENT`, `GEOCODE_CACHE_TTL`, `FCM_CREDENTIALS_FILE`, `FCM_MIN_SEVERITY`, `FCM_EVENT_TYPES`, `CORS_ORIGINS`, `WS_ALLOWED_ORIGIN`, `MCP_API_KEY`, `MCP_RATE_LIMIT_PER_MIN`.

Untuk MCP: `MCP_API_KEY` (kosong = endpoint `/mcp` nonaktif) adalah satu-satunya token Bearer untuk akses read-only; `MCP_RATE_LIMIT_PER_MIN` membatasi request per menit (default `120`, `0` menonaktifkan). Binary stdio tidak memerlukan token.

`MQTT_DEVICE_USERNAME` dan `MQTT_DEVICE_PASSWORD` wajib diisi saat memakai Docker Compose; nilainya dipakai semua device dan website virtual device. Gunakan username acak serta password panjang, bukan default contoh.

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
FCM_MIN_SEVERITY=error
FCM_EVENT_TYPES=FIRE_DETECTED,SMOKE_DETECTED,TEMPERATURE_HIGH,SOIL_DRY,DEVICE_OFFLINE,SENSOR_ERROR,LOW_BATTERY
```

`FCM_MIN_SEVERITY` menerima `error`, `warning`, atau `critical`. Urutan severity di backend: `error` < `warning` < `critical`, jadi `FCM_MIN_SEVERITY=error` tetap mengirim semua event valid. `FCM_EVENT_TYPES` adalah allowlist event yang dikirim; kosongkan/tidak set untuk memakai tujuh event default. Event di luar allowlist atau di bawah `FCM_MIN_SEVERITY` tidak dikirim.

Kanal notifikasi Android yang dipakai worker:

| Event | Channel | Sound |
| --- | --- | --- |
| `FIRE_DETECTED` | `fire_alerts_fire` | `notif_fire` |
| `SMOKE_DETECTED`, `TEMPERATURE_HIGH`, `SOIL_DRY`, `LOW_BATTERY` | `fire_alerts_warning` | `notif_smoke` |
| `SENSOR_ERROR`, `DEVICE_OFFLINE` | `fire_alerts_error` | `notif_sensor_error` |

`DEVICE_OFFLINE` sengaja memakai kanal dan suara error sensor agar notifikasi perangkat offline mudah dibedakan dari peringatan biasa.

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
  cmd/mcp/            entrypoint MCP stdio (read-only)
  cmd/simulator/      simulator perangkat
  internal/config/    konfigurasi env
  internal/domain/    model dan enum
  internal/platform/  postgres, redis, mqtt, fcm, logger
  internal/repository query database
  internal/service/   auth, command, risk engine, notifier
  internal/worker/    consumer, outbox, sweeper
  internal/transport/ HTTP handler, MCP server, WebSocket hub
  migrations/         migrasi SQL
  deploy/mosquitto/   konfigurasi broker dan provisioning
  scripts/            sinkronisasi akun MQTT bersama
```

## Deployment VPS dengan Docker Compose dan Caddy

Konfigurasi produksi memakai profil Compose `prod`; Caddy hanya berjalan saat profil tersebut diaktifkan.

1. Arahkan DNS `api.domain.com` dan `mqtt.domain.com` ke IP VPS.
2. Buka firewall hanya untuk SSH, HTTP, dan HTTPS:
   ```bash
   sudo ufw allow 22/tcp
   sudo ufw allow 80/tcp
   sudo ufw allow 443/tcp
   sudo ufw enable
   ```
3. Salin `.env.prod.example` menjadi `.env`, lalu isi domain, email ACME, password database/MQTT/JWT/admin, dan kredensial FCM.
4. Letakkan service account FCM di `deploy/fcm/service-account.json` dan pastikan file tidak masuk repository.
5. Jalankan:
   ```bash
   docker compose --profile prod up -d --build
   docker compose ps
   docker compose logs -f caddy
   ```
6. Caddy akan menerbitkan sertifikat otomatis untuk:
   - `https://api.domain.com` → API dan WebSocket `/api/v1/ws`
   - `wss://mqtt.domain.com/mqtt` → Mosquitto WebSocket untuk website virtual device
7. Di website virtual device, isi broker `wss://mqtt.domain.com/mqtt`, prefix `fire-free-forest`, device ID `FFF-VIRTUAL-DEVICE`, client ID `FFF-VIRTUAL-DEVICE`, serta username/password bersama dari `.env`.
8. Verifikasi:
   ```bash
   curl https://api.domain.com/healthz
   ```

Port `8080`, `9001`, `1883`, `5432`, dan `6379` hanya di-bind ke loopback lewat variabel `API_BIND_IP`, `MQTT_BIND_IP`, dan `MQTT_WS_BIND_IP`. Jangan buka port tersebut ke publik. Jika perangkat keras memakai MQTT langsung, tambahkan listener TLS `8883` terpisah.

Build aplikasi Android untuk produksi:

```bash
flutter build appbundle --release \
  --dart-define=API_BASE_URL=https://api.domain.com
```

Gunakan `--dart-define=ALLOW_SERVER_SWITCH=true` hanya untuk build internal yang memang perlu mengganti server saat runtime.

## Keamanan Produksi

- Ganti semua kredensial default (`JWT_SECRET`, password admin, password MQTT, password PostgreSQL).
- Set `CORS_ORIGINS` dan `WS_ALLOWED_ORIGIN` ke domain dashboard, jangan `*`.
- Gunakan TLS di reverse proxy untuk API dan WebSocket.
- Batasi akses port MQTT atau gunakan TLS listener.
- Simpan service account FCM sebagai secret, jangan commit ke repository.
- Perlakukan `MCP_API_KEY` seperti password. Simpan hanya di secret manager/`.env`, rotasi berkala dengan mengganti env lalu restart, dan biarkan kosong bila MCP tidak dipakai.
- Batasi `MCP_RATE_LIMIT_PER_MIN` sesuai beban.
- Backup volume PostgreSQL secara berkala.
