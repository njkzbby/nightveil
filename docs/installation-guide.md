# Nightveil — Installation Guide / Руководство по установке

[English](#english) | [Русский](#русский)

---

<a id="english"></a>

## English

### What you need

| Component | Required | Notes |
|-----------|----------|-------|
| VPS (Linux) | Yes | Any provider outside Russia (Hetzner, DigitalOcean, Oracle, Quins, etc.) |
| Domain name | Recommended | For Cloudflare CDN. Without it — direct connection with self-signed cert. |
| Cloudflare account | Recommended | Free plan is enough. Provides CDN stealth + collateral damage protection. |
| Windows/Mac/Linux PC | Yes | Client side |

### Option A: Docker (recommended)

**Step 1: Connect to your VPS**

```bash
ssh root@YOUR_VPS_IP
```

**Step 2: Install Docker**

```bash
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
```

**Step 3: Download Nightveil**

```bash
git clone https://github.com/njkzbby/nightveil
cd nightveil
```

**Step 4: Start the server**

Basic mode (self-signed TLS):
```bash
docker compose up -d
```

REALITY mode (probes see real google.com):
```bash
NV_DEST=google.com:443 docker compose up -d
```

Custom port (if 443 is taken):
```bash
NV_PORT=8443 docker compose up -d
```

**Step 5: Get import link**

```bash
docker compose logs nightveil
```

Look for a line like:
```
nightveil://ABC123...@85.x.x.x:443?sid=abcdef01&...#Nightveil
```

Copy this entire link.

**Step 6: Connect from your PC**

Option 1 — **V2RayN** (Windows, recommended):
1. Download nightveil-v2rayN from [releases](https://github.com/njkzbby/nightveil-v2rayN)
2. Copy the import link
3. In V2RayN: Servers → Import from clipboard
4. Select "Nightveil" server → Connect

Option 2 — **CLI**:
```bash
nv connect "nightveil://ABC123...@85.x.x.x:443?..."
```
Then configure your browser proxy to `127.0.0.1:10809` (SOCKS5).

### Option B: Manual install (without Docker)

**Step 1: Build the binary**

On your local machine:
```bash
git clone https://github.com/njkzbby/nightveil
cd nightveil
GOOS=linux GOARCH=amd64 go build -o nv-linux ./cmd/nv/
```

**Step 2: Upload to VPS**

```bash
scp nv-linux root@YOUR_VPS_IP:/root/
```

**Step 3: Run installer on VPS**

```bash
ssh root@YOUR_VPS_IP
chmod +x /root/nv-linux
/root/nv-linux init -port 443 -name "My Server" -dir /etc/nightveil
```

This generates keys, certificates, and config automatically.

**Step 4: Start as service**

```bash
cat > /etc/systemd/system/nightveil.service << 'EOF'
[Unit]
Description=Nightveil Server
After=network.target

[Service]
Type=simple
ExecStart=/root/nv-linux server -config /etc/nightveil/server.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now nightveil
```

**Step 5: Check status**

```bash
systemctl status nightveil
journalctl -u nightveil -f
```

### Option C: One-command deploy from Windows

```powershell
cd nightveil
.\deploy.ps1 root@YOUR_VPS_IP
```

This builds, uploads, installs, and starts everything.

### Adding users

```bash
# On VPS:
nv keygen -server YOUR_VPS_IP:443 -remark "Friend's Name"
```

This prints:
- Config to add to `server.yaml` (short_id + public_key)
- Import link to send to your friend

After adding the user to config:
```bash
systemctl restart nightveil   # or: docker compose restart
```

### Cloudflare CDN setup (optional, recommended)

1. Buy a cheap domain ($2-5 on Namecheap/Cloudflare)
2. Add it to Cloudflare (free plan)
3. DNS → Add A record: `@` → your VPS IP → **Proxied** (orange cloud)
4. SSL/TLS → Full (Strict)
5. SSL/TLS → Origin Server → Create Certificate → save cert.pem and key.pem
6. Upload certs to VPS:
   ```bash
   scp cert.pem key.pem root@VPS:/etc/nightveil/
   ```
7. Update server.yaml:
   ```yaml
   tls:
     cert_file: "/etc/nightveil/cert.pem"
     key_file: "/etc/nightveil/key.pem"
   ```
8. Restart: `systemctl restart nightveil`
9. In client config, change address to your domain: `address: "yourdomain.com:443"`

### Updating

Docker:
```bash
cd nightveil
git pull
docker compose build --no-cache
docker compose up -d
```

Manual:
```bash
# Rebuild and upload
GOOS=linux GOARCH=amd64 go build -o nv-linux ./cmd/nv/
scp nv-linux root@VPS:/root/
ssh root@VPS "systemctl stop nightveil; cp /root/nv-linux /opt/nightveil/nv; systemctl start nightveil"
```

Windows:
```powershell
.\deploy.ps1 root@YOUR_VPS_IP
```

### Troubleshooting

| Problem | Solution |
|---------|----------|
| Port 443 already in use | Use `NV_PORT=8443` or stop the other service |
| Connection refused | Check firewall: `ufw allow 443` or `iptables -I INPUT -p tcp --dport 443 -j ACCEPT` |
| V2RayN shows "failed to start core" | Make sure nightveil-sing-box is in `bin/sing_box/` |
| Slow speed | Try REALITY mode; check if ISP throttles; enable anti-throttle in client config |
| Disconnects every 2-3 minutes | Update to latest version (DownloadBuffer fix); check VPS network quality |

---

<a id="русский"></a>

## Русский

### Что понадобится

| Компонент | Обязательно | Примечание |
|-----------|------------|------------|
| VPS (Linux) | Да | Любой провайдер за пределами РФ (Hetzner, DigitalOcean, Oracle, Quins и т.д.) |
| Доменное имя | Рекомендуется | Для Cloudflare CDN. Без него — прямое подключение с self-signed сертификатом. |
| Аккаунт Cloudflare | Рекомендуется | Бесплатного плана достаточно. |
| ПК (Windows/Mac/Linux) | Да | Клиентская сторона |

### Вариант А: Docker (рекомендуется)

**Шаг 1: Подключитесь к VPS**

```bash
ssh root@IP_ВАШЕГО_VPS
```

**Шаг 2: Установите Docker**

```bash
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
```

**Шаг 3: Скачайте Nightveil**

```bash
git clone https://github.com/njkzbby/nightveil
cd nightveil
```

**Шаг 4: Запустите сервер**

Базовый режим (self-signed TLS):
```bash
docker compose up -d
```

REALITY режим (зонды видят настоящий google.com):
```bash
NV_DEST=google.com:443 docker compose up -d
```

Свой порт (если 443 занят):
```bash
NV_PORT=8443 docker compose up -d
```

**Шаг 5: Получите ссылку для импорта**

```bash
docker compose logs nightveil
```

Найдите строку вида:
```
nightveil://ABC123...@85.x.x.x:443?sid=abcdef01&...#Nightveil
```

Скопируйте эту ссылку целиком.

**Шаг 6: Подключитесь с ПК**

Вариант 1 — **V2RayN** (Windows, рекомендуется):
1. Скачайте nightveil-v2rayN
2. Скопируйте import link
3. В V2RayN: Серверы → Импорт из буфера обмена
4. Выберите сервер "Nightveil" → Подключиться

Вариант 2 — **CLI**:
```bash
nv connect "nightveil://ABC123...@85.x.x.x:443?..."
```
Настройте прокси в браузере: `127.0.0.1:10809` (SOCKS5).

### Вариант Б: Ручная установка

**Шаг 1: Соберите бинарник**

```bash
git clone https://github.com/njkzbby/nightveil
cd nightveil
GOOS=linux GOARCH=amd64 go build -o nv-linux ./cmd/nv/
```

**Шаг 2: Загрузите на VPS**

```bash
scp nv-linux root@IP_VPS:/root/
```

**Шаг 3: Инициализация на VPS**

```bash
ssh root@IP_VPS
chmod +x /root/nv-linux
/root/nv-linux init -port 443 -name "Мой сервер" -dir /etc/nightveil
```

**Шаг 4: Запуск как сервис**

```bash
cat > /etc/systemd/system/nightveil.service << 'EOF'
[Unit]
Description=Nightveil Server
After=network.target

[Service]
Type=simple
ExecStart=/root/nv-linux server -config /etc/nightveil/server.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now nightveil
```

**Шаг 5: Проверка**

```bash
systemctl status nightveil
journalctl -u nightveil -f
```

### Вариант В: Деплой одной командой из Windows

```powershell
cd nightveil
.\deploy.ps1 root@IP_VPS
```

### Добавление пользователей

```bash
nv keygen -server IP_VPS:443 -remark "Имя друга"
```

Команда выведет:
- Конфиг для добавления в `server.yaml`
- Import link для отправки другу

После добавления:
```bash
systemctl restart nightveil   # или: docker compose restart
```

### Настройка Cloudflare CDN (опционально, рекомендуется)

1. Купите домен ($2-5 на Namecheap/Cloudflare)
2. Добавьте в Cloudflare (бесплатный план)
3. DNS → A запись: `@` → IP вашего VPS → **Proxied** (оранжевое облако)
4. SSL/TLS → Full (Strict)
5. SSL/TLS → Origin Server → Create Certificate → сохраните cert.pem и key.pem
6. Загрузите на VPS:
   ```bash
   scp cert.pem key.pem root@VPS:/etc/nightveil/
   ```
7. Обновите server.yaml:
   ```yaml
   tls:
     cert_file: "/etc/nightveil/cert.pem"
     key_file: "/etc/nightveil/key.pem"
   ```
8. Перезапустите: `systemctl restart nightveil`
9. В клиенте измените адрес на домен: `address: "yourdomain.com:443"`

### Обновление

Docker:
```bash
cd nightveil && git pull
docker compose build --no-cache && docker compose up -d
```

Ручное:
```powershell
.\deploy.ps1 root@IP_VPS
```

### Решение проблем

| Проблема | Решение |
|----------|---------|
| Порт 443 занят | Используйте `NV_PORT=8443` или остановите другой сервис |
| Connection refused | Проверьте firewall: `ufw allow 443` |
| V2RayN: "не удалось запустить ядро" | Убедитесь что nightveil-sing-box в `bin/sing_box/` |
| Медленная скорость | Попробуйте REALITY; проверьте throttling провайдера |
| Отключается каждые 2-3 минуты | Обновите до последней версии (фикс DownloadBuffer) |
