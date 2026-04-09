# Nightveil

<!--
  Replace this comment with a logo or banner image:
  ![Nightveil Banner](docs/banner.png)
-->

[![Go Version](https://img.shields.io/badge/go-1.25-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-221%20passing-brightgreen.svg)](#testing)
[![Build](https://img.shields.io/badge/build-passing-brightgreen.svg)](#building-from-source)

**Nightveil** is a censorship-resistant proxy protocol designed to defeat Russia's TSPU (Технические Средства Противодействия Угрозам) deep packet inspection system. It combines XHTTP stealth transport, REALITY TLS camouflage, and a layered anti-throttling system to make proxy traffic indistinguishable from normal HTTPS browsing — at every layer the censor inspects.

---

## Contents

- [Features](#features)
- [Quick Start — Docker](#quick-start--docker)
- [Manual Installation](#manual-installation)
- [Building from Source](#building-from-source)
- [Configuration Reference](#configuration-reference)
- [CLI Reference](#cli-reference)
- [Import Link Format](#import-link-format)
- [Architecture](#architecture)
- [Security Model](#security-model)
- [TSPU Threat Coverage](#tspu-threat-coverage)
- [Testing](#testing)
- [Related Repositories](#related-repositories)
- [Contributing](#contributing)
- [License](#license)

---

## Features

### Stealth Transport
- **XHTTP packet-up** — splits upload and download into separate HTTP transactions. POST requests carry upload chunks under 14 KB; GET responses stream download data. To TSPU, it looks like ordinary web browsing.
- **uTLS fingerprint mimicry** — TLS ClientHello mimics Chrome, Firefox, or Safari. Go's built-in TLS stack is never exposed.
- **REALITY mode** — real certificate served from a target domain (e.g. `google.com`). Active probes see a legitimate TLS handshake with a real certificate, not a proxy.
- **Fallback website** — HTTP/S traffic from probes not carrying valid credentials is forwarded to a real website or served as a static site, defeating active-probing detection.

### Anti-Throttling
- Throttle detection via RTT spike and throughput-drop sensors
- Adaptive connection multiplexing — opens additional connections when throttling is detected
- Connection rotation with canary probing to find unthrottled paths
- Per-client unique paths, session key names, and chunk sizes that rotate every 5–30 minutes, preventing TSPU from building stable fingerprints

### Cryptography
- **Double ECDH** — X25519 ephemeral key exchange combined with a per-user X25519 key pair, giving both forward secrecy and per-user authentication in a single handshake
- **HKDF-SHA256 + ChaCha20-Poly1305 AEAD** — authenticated encryption with a random nonce for every message
- **Per-user keys** — each user has their own X25519 key pair; revoking one user does not affect others
- Timestamp replay protection (configurable window, default 120 s)

### Traffic Shaping and Cover
- Composable middleware: padding, RTT jitter injection, traffic shaping profiles (browsing / streaming / idle)
- Cover traffic generation to maintain a realistic traffic baseline during idle periods
- Random padding added to every message; chunk sizes are configurable per user

### Protocol
- Full-duplex proxy over XHTTP with a frame protocol (CONNECT / ACK / DATA / CLOSE / UDP)
- UDP relay — Discord voice/video and other UDP applications work transparently
- DownloadBuffer with offset tracking — zero data loss on reconnect after a dropped connection
- Transport Manager with automatic failover between transport backends

### Deployment
- Single binary (`nv`) with subcommands: `server`, `connect`, `keygen`, `init`
- Docker with auto-initialization — runs `nv init` on first start, prints an import link to logs
- `deploy.ps1` for one-command deployment to a remote VPS from Windows
- Interactive `install.sh` for Linux bare-metal / VM installs with systemd
- Management API for runtime status and user provisioning
- V2RayN native protocol support via `nightveil://` URI import
- sing-box outbound adapter

---

## Quick Start — Docker

### Basic (self-signed TLS)

```bash
# Clone or copy the repository to your server
git clone https://github.com/nightveil/nv
cd nv

# Start. On first run the server initialises itself and prints an import link.
docker compose up -d

# View the import link
docker compose logs nightveil
```

### REALITY mode

REALITY mode forwards TLS probes to a real destination site. The censor sees a valid certificate from that site and cannot distinguish your server from a legitimate host.

```bash
NV_DEST=google.com:443 docker compose up -d
docker compose logs nightveil
```

### Custom port

```bash
NV_PORT=8443 docker compose up -d
```

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NV_PORT` | `443` | TCP/UDP port to listen on |
| `NV_NAME` | `Nightveil` | Display name in the generated import link |
| `NV_DEST` | *(empty)* | REALITY destination, e.g. `google.com:443`. Enables REALITY mode when set. |

### Add a user to a running server

```bash
docker compose exec nightveil nv keygen -server YOUR_HOST:443 -remark "Alice"
```

The command prints the config snippet to add to `server.yaml` and a ready-to-import link. Restart the server after editing the config:

```bash
docker compose restart nightveil
```

### Rebuild after an update

```bash
docker compose build --no-cache && docker compose up -d
```

---

## Manual Installation

### Deploy to a VPS from Windows

```powershell
.\deploy.ps1 root@your-vps
```

This copies the binary, runs the installer, and prints the import link.

### Linux interactive installer

Upload the `nv-linux` binary to your server, then run:

```bash
bash deploy/install.sh
```

The script installs the binary to `/opt/nightveil/`, writes a systemd unit, and walks you through first-time configuration. Run it again at any time to access the management menu (add users, change port, view logs, show import links).

### Systemd service management

```bash
systemctl status nightveil
systemctl restart nightveil
journalctl -u nightveil -f
```

---

## Building from Source

**Requirements:** Go 1.25 or later.

```bash
git clone https://github.com/nightveil/nv
cd nv
go build -o nv ./cmd/nv/
```

Cross-compile for Linux (from any OS):

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o nv-linux ./cmd/nv/
```

Cross-compile for Windows:

```bash
GOOS=windows GOARCH=amd64 go build -o nv.exe ./cmd/nv/
```

---

## Configuration Reference

### Server — `server.yaml`

```yaml
server:
  listen: "0.0.0.0:443"

  tls:
    cert_file: "/etc/nightveil/cert.pem"
    key_file:  "/etc/nightveil/key.pem"
    # REALITY mode — forward probes to this host; censor sees real certificate:
    # dest: "google.com:443"

  auth:
    private_key: "BASE64_SERVER_PRIVATE_KEY"
    max_time_diff: 120  # seconds; replay protection window
    users:
      - short_id:  "abcdef01"
        public_key: "BASE64_USER_PUBLIC_KEY"
        name:       "Alice"
      - short_id:  "12345678"
        public_key: "BASE64_USER_PUBLIC_KEY_2"
        name:       "Bob"

  transport:
    type: "xhttp"
    max_chunk_size: 14336   # bytes; keep under TSPU 15-20 KB threshold
    session_timeout: 30     # seconds

  middleware:
    - type: "padding"
      min_bytes: 64
      max_bytes: 256

  fallback:
    mode: "default"   # reverse-proxy or static site for unauthenticated requests

  # Optional management API:
  # api:
  #   enabled: true
  #   listen: "127.0.0.1:9090"
  #   secret:  "your-api-secret"
```

### Client — `client.yaml`

```yaml
client:
  inbound:
    type:   "socks5"
    listen: "127.0.0.1:10809"

  server:
    address: "your-server.com:443"

  auth:
    server_public_key: "BASE64_SERVER_PUBLIC_KEY"
    user_private_key:  "BASE64_YOUR_PRIVATE_KEY"
    short_id:          "abcdef01"

  transport:
    type: "xhttp"
    # Per-client unique params generated by `nv keygen`:
    path_prefix:      "/abc123"
    upload_path:      "/u/xyz"
    download_path:    "/d/xyz"
    session_key_name: "skey5"
    max_chunk_size:   14336

  tls:
    fingerprint: "chrome"   # chrome | firefox | safari | randomized
    # sni: "custom-sni.com"   # override SNI for CDN or REALITY
    # skip_verify: true       # only for self-signed certs in testing

  middleware:
    - type: "padding"
      min_bytes: 64
      max_bytes: 256

  anti_throttle:
    enabled: true
    detect_rtt_spike_ms:        500
    detect_throughput_drop_pct: 70
    response: "both"   # multiplex | rotate | both
```

### Key fields

| Field | Description |
|-------|-------------|
| `auth.private_key` | Server X25519 private key (base64). Generated by `nv keygen`. |
| `auth.users[].public_key` | Per-user X25519 public key. Each user gets their own. |
| `auth.users[].short_id` | 4-byte hex identifier included in each request. |
| `auth.max_time_diff` | Seconds allowed between client timestamp and server clock. Prevents replay attacks. |
| `tls.dest` | REALITY destination hostname:port. Enables REALITY mode. |
| `transport.max_chunk_size` | Maximum POST body size in bytes. Keep below 14 336 to stay under TSPU's 15–20 KB trigger. |
| `fallback.mode` | How to handle unauthenticated requests: `default` (built-in page), `reverse-proxy`, or `static`. |

---

## CLI Reference

```
nv <command> [options]

Commands:
  server    Start the Nightveil server
  connect   Connect to a server via URI or config file
  keygen    Generate a new server or add a user
  init      Initialise a server directory (keys, certs, config)
  status    Show connection status
  version   Show version

Examples:
  nv server  -config /etc/nightveil/server.yaml
  nv connect "nightveil://SERVERPUB@host:443?sid=abcdef01&...#Alice"
  nv connect -config client.yaml
  nv keygen  -server example.com:443 -remark "Alice"
  nv keygen  -server example.com:443 -pubkey SERVER_PUB -remark "Bob"
```

### `nv keygen` — first server setup

```bash
nv keygen -server example.com:443 -remark "Alice"
```

Generates a server key pair and the first user key pair, prints the server config snippet and an import link.

### `nv keygen` — add a user to an existing server

```bash
nv keygen -server example.com:443 -pubkey SERVER_PUB_KEY -remark "Bob"
```

Generates a new user key pair only. Add the printed `short_id` + `public_key` to `server.yaml`, then restart the server.

---

## Import Link Format

Nightveil uses a URI scheme for one-click import into V2RayN and compatible clients.

```
nightveil://SERVER_PUB_KEY@HOST:PORT?sid=SHORT_ID&path=/prefix&up=/u/path&down=/d/path&skey=KEY&chunk=14336&fp=chrome&upk=USER_PRIV_KEY#Remark
```

| Parameter | Description |
|-----------|-------------|
| `SERVER_PUB_KEY` | Server X25519 public key (base64url, in the userinfo position) |
| `HOST:PORT` | Server address |
| `sid` | Short ID (8 hex chars) identifying the user |
| `path` | HTTP path prefix |
| `up` | Upload path (POST endpoint) |
| `down` | Download path (GET streaming endpoint) |
| `skey` | Session key header name |
| `chunk` | Maximum chunk size in bytes |
| `fp` | TLS fingerprint: `chrome`, `firefox`, `safari`, `randomized` |
| `upk` | User X25519 private key (base64url). Embedded for convenient import; treat as a secret. |
| `#Remark` | Display name shown in the client |

`nv keygen` generates a complete, ready-to-use import link. Copy it as-is into V2RayN's "Import from clipboard" dialog or paste it to `nv connect`.

---

## Architecture

### Overview

```
Client side
  Application
      │ SOCKS5
  Protocol layer (frame: CONNECT/ACK/DATA/CLOSE/UDP)
      │
  Middleware chain (padding → jitter → shaping → cover traffic)
      │
  Transport Manager (failover across transports)
      │
  XHTTP transport (POST upload chunks / GET streaming download)
      │
  TLS (uTLS — Chrome/Firefox/Safari fingerprint)
      │ TCP / CDN
─────────────────────────────────────────────────────────────
Server side
  TCP listener
      │ TLS (own cert or REALITY)
  HTTP router
      │
  Auth (double ECDH handshake, timestamp check, short_id lookup)
      │
  XHTTP session manager (DownloadBuffer with offset tracking)
      │
  Protocol layer
      │ TCP/UDP
  Target host (e.g. google.com)
```

### Key components

| Package | Role |
|---------|------|
| `cmd/nv` | CLI entry point — `server`, `connect`, `keygen`, `init` subcommands |
| `internal/transport/xhttp` | XHTTP packet-up implementation: separate POST/GET channels |
| `internal/transport/quictun` | QUIC transport with port hopping and obfuscation (planned) |
| `internal/transport` | Transport Manager with connection pooling and failover |
| `internal/middleware` | Composable chain: padding, jitter, traffic shaping, cover traffic |
| `internal/protocol` | Frame codec: CONNECT / ACK / DATA / CLOSE / UDP |
| `internal/crypto/auth` | X25519 double-ECDH + HKDF-SHA256 + ChaCha20-Poly1305 |
| `internal/security` | TLS server, uTLS client, REALITY mode |
| `internal/session` | Session manager + DownloadBuffer (offset-tracked, lossless reconnect) |
| `internal/throttle` | Throttle detector, adaptive multiplexer, connection rotator, canary prober |
| `internal/proxy` | SOCKS5 inbound, TCP relay, UDP relay |
| `internal/fallback` | Reverse-proxy or static-site fallback for unauthenticated requests |
| `internal/config` | YAML config loader, URI parser, `nv init` defaults |
| `internal/api` | Management API: status, add user |
| `adapter/singbox` | sing-box outbound plugin |
| `pkg` | Public API for external consumers |
| `deploy` | `install.sh`, `setup.sh` for Linux installs |

### XHTTP packet-up

TSPU's behavioural rules flag connections where uploads and downloads happen simultaneously on the same TCP stream — a pattern characteristic of conventional proxy protocols. XHTTP packet-up separates these into two independent HTTP transactions:

- **Upload channel** — a series of HTTP POST requests, each carrying a data chunk up to 14 KB. POST is a normal web action.
- **Download channel** — a long-lived HTTP GET with a chunked or streaming response. Indistinguishable from loading a large webpage or streaming media.

The two channels are correlated by a session token in an HTTP header. From the outside, the connection pattern matches a web browser uploading form data and streaming a response.

---

## Security Model

### Authentication handshake

Every connection begins with a double ECDH handshake:

1. The client generates an ephemeral X25519 key pair.
2. The client performs two ECDH operations:
   - **Ephemeral × server static**: provides forward secrecy. Compromise of the server's long-term key does not expose past sessions.
   - **User static × server static**: proves user identity. Each user has a unique key pair; the server looks up the short_id to find the corresponding public key.
3. Both shared secrets are combined via HKDF-SHA256 to derive a session key.
4. All subsequent frames are encrypted with ChaCha20-Poly1305 AEAD using the session key and a random nonce.

### Replay protection

Each handshake includes a client timestamp. The server rejects any handshake where the timestamp differs from the server clock by more than `max_time_diff` seconds (default: 120 s).

### Per-user keys and revocation

Every user has their own X25519 key pair. To revoke access, remove the user's entry from `server.yaml` and restart the server. Other users are unaffected because their keys are independent.

### REALITY mode

When `tls.dest` is set, the server uses the destination host's real TLS certificate for the handshake. This is accomplished by forwarding the TLS ClientHello to the real host, extracting its certificate, and presenting it. Clients that know the server's public key can complete the Nightveil handshake; probes that do not know the key see a legitimate TLS session and a real website via the fallback handler.

### Threat summary

| Threat | Mitigation |
|--------|-----------|
| Passive TLS fingerprinting | uTLS mimics real browser fingerprints |
| Active probing — certificate | REALITY serves real certificate |
| Active probing — content | Fallback website returns real content |
| SNI-based blocking | Custom SNI; CDN hides origin |
| Long-term key compromise | Ephemeral ECDH provides forward secrecy |
| Replay attacks | 120-second timestamp window |
| Per-user tracking / mass revocation | Independent per-user key pairs |
| Brute-force enumeration | No valid response without correct key material |

---

## TSPU Threat Coverage

Russia's TSPU operates across multiple protocol layers simultaneously. The table below maps each known detection technique to the Nightveil countermeasure.

| Layer | TSPU technique | Nightveil countermeasure |
|-------|---------------|--------------------------|
| L1 — IP/transport | IP protocol signatures | Standard TCP/HTTPS; optionally routed through Cloudflare CDN |
| L2 — TLS | JA3/JA4 TLS fingerprint | uTLS Chrome/Firefox/Safari ClientHello mimicry |
| L2 — TLS | SNI-based domain block | Configurable SNI; CDN masks origin IP |
| L3 — Application | Active probing — TLS certificate | REALITY mode: probes receive real certificate from target domain |
| L3 — Application | Active probing — HTTP content | Fallback site returns real HTML; passes manual and automated probing |
| L3 — Routing | ASN / IP mismatch detection | Cloudflare CDN: origin IP belongs to Cloudflare ASN |
| L4 — Behavioural | Bidirectional symmetric traffic (proxy pattern) | XHTTP packet-up: upload and download are separate, asymmetric HTTP transactions |
| L4 — Packet sizing | 15–20 KB chunk size threshold | Max chunk size 14 336 bytes, configurable per user |
| L4 — Packet sizing | Uniform padding fingerprint | Random padding per message (64–256 bytes by default) |
| L4 — Throttling | Bandwidth throttling on detected proxies | Adaptive multiplexing; connection rotation; canary-based path selection |
| L4 — Fingerprinting | Stable per-client path/parameter fingerprint | Paths, padding, chunk sizes rotate every 5–30 minutes |
| L4 — Timing | Cross-layer RTT correlation | RTT jitter injection; traffic shaping profiles |
| L4 — Idle traffic | Silence-based detection | Cover traffic generation during idle periods |
| Go runtime | Go TLS stack fingerprint | TLS terminated at CDN; Go process hidden behind CDN edge |

---

## Testing

The test suite contains 221 tests covering the full stack.

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run a specific package
go test ./internal/crypto/auth/...
go test ./internal/throttle/...
go test ./internal/security/...

# Run with race detector
go test -race ./...
```

Key test coverage areas:

- `internal/crypto/auth` — X25519 key generation, double-ECDH handshake, HKDF derivation, AEAD round-trips
- `internal/security` — uTLS fingerprint generation, REALITY handshake (end-to-end)
- `internal/throttle` — throttle detection, canary probing, connection rotation, adaptive multiplexing
- `internal/transport` — XHTTP session management, reconnect and offset recovery, transport manager failover
- `adapter/singbox` — sing-box outbound integration

---

## Related Repositories

| Repository | Description |
|------------|-------------|
| **nightveil** (this repo) | Core protocol library, server, and CLI |
| **nightveil-sing-box** | Fork of sing-box with a native Nightveil outbound. Drop-in replacement for standard sing-box. |
| **nightveil-v2rayN** | Fork of V2RayN with native `nightveil://` protocol support and one-click import. |

Client-side integration is also possible via `pkg/` — the public Go API — for embedding Nightveil in other applications.

---

## Contributing

Contributions are welcome. Please follow these guidelines:

1. **Open an issue first** for any non-trivial change so we can discuss the approach before implementation.
2. **Tests are required.** Every new feature or bug fix must include tests. PRs without adequate test coverage will not be merged.
3. **Keep scope tight.** Each PR should address a single concern.
4. **Security issues** should be reported privately. Do not open a public issue for vulnerabilities. Email the maintainers directly or use GitHub's private security advisory feature.

### Development setup

```bash
git clone https://github.com/nightveil/nv
cd nv
go build ./...
go test ./...
```

### Code style

- Standard `gofmt` formatting enforced
- `go vet` must pass with no warnings
- Race detector (`go test -race`) must pass

---

## License

This project is licensed under the GNU General Public License v3.0. See [LICENSE](LICENSE) for the full text.

---

> Nightveil is a research and personal-use tool. Users are responsible for understanding and complying with applicable laws in their jurisdiction. The authors provide this software as-is, without warranty of any kind.

---

<details>
<summary><h1>Документация на русском языке</h1></summary>

# Nightveil

**Nightveil** — антицензурный прокси-протокол, разработанный для обхода российской системы DPI (ТСПУ). Комбинирует XHTTP-транспорт (трафик выглядит как обычный веб-браузинг), REALITY-камуфляж TLS и многоуровневую систему anti-throttling.

---

## Возможности

### Скрытный транспорт
- **XHTTP packet-up** — разделяет upload и download на отдельные HTTP-транзакции. POST-запросы несут чанки до 14 КБ; GET-ответы стримят данные. Для ТСПУ это выглядит как обычный веб-сайт.
- **uTLS** — TLS-хендшейк имитирует Chrome, Firefox или Safari. Go-шный TLS-стек никогда не виден.
- **REALITY** — сервер показывает реальный сертификат целевого домена (например `google.com`). Active probing видит легитимный TLS.
- **Fallback-сайт** — неавторизованные запросы получают реальный веб-сайт.

### Anti-Throttling
- Детекция throttling по RTT-спайкам и падению throughput
- Адаптивное мультиплексирование — открывает дополнительные соединения при throttling
- Ротация соединений с canary-проверкой
- Per-client уникальные параметры (пути, padding, chunk sizes) ротируются каждые 5–30 минут

### Криптография
- **Double ECDH** — X25519 эфемерный ключ + per-user ключ × серверный ключ
- **HKDF-SHA256 + ChaCha20-Poly1305 AEAD** — рандомный nonce для каждого сообщения
- **Per-user ключи** — у каждого пользователя свой X25519 keypair, индивидуальный отзыв
- Защита от replay-атак (временное окно 120 секунд)

### Traffic Shaping
- Composable middleware: padding, RTT jitter, traffic shaping (browsing/streaming/idle)
- Cover traffic — генерация фонового трафика в периоды простоя
- Рандомный padding на каждое сообщение

### Протокол
- Full-duplex прокси через XHTTP с фреймами (CONNECT / ACK / DATA / CLOSE / UDP)
- UDP relay — Discord голос/видео работают
- DownloadBuffer с offset tracking — нулевая потеря данных при reconnect
- Transport Manager с автоматическим failover

### Деплой
- Один бинарник (`nv`) с подкомандами: `server`, `connect`, `keygen`, `init`
- Docker с автоинициализацией — `nv init` при первом запуске, import link в логах
- `deploy.ps1` — деплой на VPS одной командой из Windows
- `install.sh` — интерактивный установщик для Linux с systemd
- API для управления и мониторинга
- Нативная поддержка в V2RayN через `nightveil://` URI
- sing-box outbound адаптер

---

## Быстрый старт — Docker

### Базовый (self-signed TLS)

```bash
git clone https://github.com/njkzbby/nightveil
cd nightveil
docker compose up -d
docker compose logs nightveil   # ← здесь import link
```

### REALITY режим

```bash
NV_DEST=google.com:443 docker compose up -d
```

### Свой порт

```bash
NV_PORT=8443 docker compose up -d
```

### Переменные окружения

| Переменная | По умолчанию | Описание |
|------------|-------------|----------|
| `NV_PORT` | `443` | TCP/UDP порт |
| `NV_NAME` | `Nightveil` | Имя в import link |
| `NV_DEST` | *(пусто)* | REALITY destination. Включает REALITY когда задано. |

### Добавить пользователя

```bash
docker compose exec nightveil nv keygen -server ХОСТ:443 -remark "Имя"
```

Команда выводит конфиг для `server.yaml` и готовую ссылку для импорта. После редактирования конфига:

```bash
docker compose restart nightveil
```

---

## Ручная установка

### Деплой на VPS из Windows

```powershell
.\deploy.ps1 root@your-vps
```

### Интерактивный установщик Linux

```bash
bash deploy/install.sh
```

### Управление через systemd

```bash
systemctl status nightveil
systemctl restart nightveil
journalctl -u nightveil -f
```

---

## Сборка из исходников

```bash
git clone https://github.com/njkzbby/nightveil
cd nightveil
go build -o nv ./cmd/nv/
```

Кросс-компиляция для Linux:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o nv-linux ./cmd/nv/
```

---

## Формат import link

```
nightveil://СЕРВЕРНЫЙ_КЛЮЧ@ХОСТ:ПОРТ?sid=SHORT_ID&path=/префикс&up=/u/путь&down=/d/путь&skey=ключ&chunk=14336&fp=chrome&upk=ПРИВАТНЫЙ_КЛЮЧ_ЮЗЕРА#Название
```

| Параметр | Описание |
|----------|----------|
| `СЕРВЕРНЫЙ_КЛЮЧ` | Публичный X25519 ключ сервера (base64url) |
| `ХОСТ:ПОРТ` | Адрес сервера |
| `sid` | Short ID пользователя (8 hex символов) |
| `path` | Префикс HTTP-пути |
| `up` | Путь для upload (POST) |
| `down` | Путь для download (GET streaming) |
| `skey` | Имя HTTP-заголовка для session key |
| `chunk` | Максимальный размер чанка в байтах |
| `fp` | TLS fingerprint: `chrome`, `firefox`, `safari`, `randomized` |
| `upk` | Приватный X25519 ключ пользователя (base64url) |
| `#Название` | Отображаемое имя в клиенте |

---

## Покрытие угроз ТСПУ

| Слой | Техника ТСПУ | Защита Nightveil |
|------|-------------|------------------|
| L1 — Сигнатуры | Сигнатуры IP-протоколов | Стандартный TCP/HTTPS; опционально через CDN |
| L2 — TLS | JA3/JA4 TLS fingerprint | uTLS мимикрия Chrome/Firefox/Safari |
| L2 — TLS | Блокировка по SNI | Настраиваемый SNI; CDN скрывает origin |
| L3 — Active probing | Проверка TLS-сертификата | REALITY: зонды получают реальный сертификат |
| L3 — Active probing | Проверка HTTP-контента | Fallback-сайт отдаёт реальный HTML |
| L3 — Маршрутизация | ASN/IP несоответствие | CDN: IP принадлежит Cloudflare |
| L4 — Поведение | Симметричный bidirectional трафик | XHTTP: upload и download — отдельные HTTP-транзакции |
| L4 — Размеры | Порог 15-20 КБ | Чанки до 14 336 байт |
| L4 — Размеры | Единообразный padding | Рандомный padding 64-256 байт |
| L4 — Throttling | Замедление прокси | Адаптивный multiplexing + ротация соединений |
| L4 — Фингерпринт | Стабильные параметры клиента | Пути, padding, chunk sizes ротируются каждые 5-30 мин |
| L4 — Тайминг | Cross-layer RTT корреляция | RTT jitter + traffic shaping |
| L4 — Idle | Детекция по тишине | Cover traffic в периоды простоя |
| Go runtime | Fingerprint Go TLS стека | TLS терминируется на CDN; Go скрыт за CDN |

---

## Тестирование

221 тест покрывает весь стек:

```bash
go test ./...           # все тесты
go test -v ./...        # подробный вывод
go test -race ./...     # race detector
```

---

## Связанные репозитории

| Репозиторий | Описание |
|-------------|----------|
| **nightveil** (этот) | Core протокол, сервер и CLI |
| **nightveil-sing-box** | Форк sing-box с Nightveil outbound |
| **nightveil-v2rayN** | Форк V2RayN с нативной поддержкой `nightveil://` |

---

## Лицензия

Проект распространяется под лицензией GNU General Public License v3.0. См. [LICENSE](LICENSE).

---

> Nightveil — инструмент для исследований и личного использования. Пользователи несут ответственность за соблюдение законодательства своей юрисдикции. Авторы предоставляют ПО «как есть», без каких-либо гарантий.

</details>
