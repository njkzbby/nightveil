#!/bin/bash
# Nightveil Server — Interactive Installer & Manager
# Fresh install:  bash install.sh
# Management:     bash install.sh (when already installed)
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

INSTALL_DIR="/opt/nightveil"
SERVICE="/etc/systemd/system/nightveil.service"

header() {
    echo ""
    echo -e "  ${CYAN}================================${NC}"
    echo -e "  ${CYAN} Nightveil Server${NC}"
    echo -e "  ${CYAN}================================${NC}"
    echo ""
}

# ---- Management Functions ----

do_update() {
    echo -e "  ${YELLOW}Updating binary...${NC}"
    if [ -f /root/nv-linux ]; then
        cp /root/nv-linux "$INSTALL_DIR/nv"
        chmod +x "$INSTALL_DIR/nv"
        systemctl restart nightveil
        echo -e "  ${GREEN}Updated and restarted!${NC}"
    else
        echo -e "  ${RED}Upload nv-linux to /root/ first${NC}"
    fi
}

do_adduser() {
    SERVER_IP=$(curl -s4 ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')
    PORT=$(grep "listen:" "$INSTALL_DIR/server.yaml" | grep -oP '\d+' | head -1)

    # Get existing pubkey — derive from private key
    PRIV=$(grep "private_key:" "$INSTALL_DIR/server.yaml" | awk '{print $2}' | tr -d '"')

    read -p "  User name [Friend]: " NAME
    NAME=${NAME:-Friend}

    echo ""
    "$INSTALL_DIR/nv" keygen -server "$SERVER_IP:$PORT" -pubkey "$PRIV" -remark "$NAME" 2>&1 || \
    "$INSTALL_DIR/nv" keygen -server "$SERVER_IP:$PORT" -remark "$NAME" 2>&1

    echo ""
    echo -e "  ${YELLOW}Add the new shortID to $INSTALL_DIR/server.yaml under short_ids:${NC}"
    echo -e "  ${YELLOW}Then: systemctl restart nightveil${NC}"
}

do_status() {
    echo -e "  ${CYAN}Service status:${NC}"
    systemctl status nightveil --no-pager -l 2>/dev/null || echo "  Not running"
    echo ""
    echo -e "  ${CYAN}Config:${NC}"
    grep -E "listen:|private_key:|short_ids:" "$INSTALL_DIR/server.yaml" 2>/dev/null
    echo ""
    echo "  Full logs: journalctl -u nightveil -f"
}

do_showlink() {
    SERVER_IP=$(curl -s4 ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')
    PORT=$(grep "listen:" "$INSTALL_DIR/server.yaml" | grep -oP '\d+' | head -1)
    echo ""
    echo -e "  ${CYAN}Server:${NC} $SERVER_IP:$PORT"
    echo ""
    echo -e "  ${CYAN}To generate import link for a user:${NC}"
    echo "  $INSTALL_DIR/nv keygen -server $SERVER_IP:$PORT -remark \"Name\""
    echo ""
}

do_port() {
    CURRENT=$(grep "listen:" "$INSTALL_DIR/server.yaml" | grep -oP '\d+' | head -1)
    echo -e "  Current port: ${GREEN}$CURRENT${NC}"
    read -p "  New port: " NEW_PORT
    if [ -n "$NEW_PORT" ]; then
        sed -i "s|:$CURRENT\"|:$NEW_PORT\"|" "$INSTALL_DIR/server.yaml"
        systemctl restart nightveil
        echo -e "  ${GREEN}Changed to :$NEW_PORT and restarted${NC}"
        echo -e "  ${YELLOW}Update client configs to use the new port!${NC}"
    fi
}

do_uninstall() {
    read -p "  Remove Nightveil completely? [y/N]: " CONFIRM
    if [ "$CONFIRM" = "y" ] || [ "$CONFIRM" = "Y" ]; then
        systemctl stop nightveil 2>/dev/null || true
        systemctl disable nightveil 2>/dev/null || true
        rm -f "$SERVICE"
        rm -rf "$INSTALL_DIR"
        systemctl daemon-reload
        echo -e "  ${GREEN}Uninstalled.${NC}"
    fi
}

# ---- Management Menu ----
if [ -f "$INSTALL_DIR/nv" ] && [ -f "$INSTALL_DIR/server.yaml" ]; then
    header
    echo -e "  ${GREEN}Nightveil is installed${NC}"
    echo ""
    echo "  1) Update binary"
    echo "  2) Add user"
    echo "  3) Show status"
    echo "  4) Show server info"
    echo "  5) Change port"
    echo "  6) Restart"
    echo "  7) View logs"
    echo "  8) Reinstall from scratch"
    echo "  9) Uninstall"
    echo "  0) Exit"
    echo ""
    read -p "  Choose [0]: " CHOICE
    CHOICE=${CHOICE:-0}

    case $CHOICE in
        1) do_update ;;
        2) do_adduser ;;
        3) do_status ;;
        4) do_showlink ;;
        5) do_port ;;
        6) systemctl restart nightveil && echo -e "  ${GREEN}Restarted${NC}" ;;
        7) journalctl -u nightveil -f ;;
        8) ;; # fall through to fresh install
        9) do_uninstall; exit 0 ;;
        0) exit 0 ;;
        *) exit 0 ;;
    esac

    [ "$CHOICE" != "8" ] && exit 0
fi

# ---- Fresh Install ----

header
echo -e "  ${CYAN}New installation${NC}"
echo ""

# Detect IP
SERVER_IP=$(curl -s4 ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')
echo -e "  Server IP: ${GREEN}$SERVER_IP${NC}"
echo ""

# Port
read -p "  Port [443]: " NV_PORT
NV_PORT=${NV_PORT:-443}

# Name
read -p "  Server name [Nightveil]: " NV_NAME
NV_NAME=${NV_NAME:-Nightveil}

# TLS
echo ""
echo "  TLS mode:"
echo "    1) Self-signed (quick start)"
echo "    2) No TLS (behind Nginx/CDN)"
echo "    3) Custom certificate"
read -p "  Choice [1]: " TLS_CHOICE
TLS_CHOICE=${TLS_CHOICE:-1}

TLS_CERT=""
TLS_KEY=""
TLS_BLOCK="  tls: {}"

mkdir -p "$INSTALL_DIR"

case $TLS_CHOICE in
    1)
        openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
            -keyout "$INSTALL_DIR/key.pem" -out "$INSTALL_DIR/cert.pem" \
            -days 3650 -nodes -subj "/CN=nightveil" \
            -addext "subjectAltName=IP:$SERVER_IP" 2>/dev/null
        TLS_CERT="$INSTALL_DIR/cert.pem"
        TLS_KEY="$INSTALL_DIR/key.pem"
        TLS_BLOCK="  tls:
    cert_file: \"$TLS_CERT\"
    key_file: \"$TLS_KEY\""
        echo -e "  ${GREEN}Self-signed cert generated${NC}"
        ;;
    2)
        echo -e "  ${YELLOW}No TLS${NC}"
        ;;
    3)
        read -p "  Cert file path: " TLS_CERT
        read -p "  Key file path: " TLS_KEY
        TLS_BLOCK="  tls:
    cert_file: \"$TLS_CERT\"
    key_file: \"$TLS_KEY\""
        ;;
esac

# Install binary
if [ -f /root/nv-linux ]; then
    cp /root/nv-linux "$INSTALL_DIR/nv"
    chmod +x "$INSTALL_DIR/nv"
    echo -e "  ${GREEN}Binary installed${NC}"
else
    echo -e "  ${RED}ERROR: /root/nv-linux not found${NC}"
    echo "  Upload: scp nv-linux root@$SERVER_IP:/root/"
    exit 1
fi

# Generate keys
echo ""
echo -e "  ${YELLOW}Generating keys...${NC}"
KEYGEN_OUT=$("$INSTALL_DIR/nv" keygen -server "$SERVER_IP:$NV_PORT" -remark "$NV_NAME" 2>&1)

PRIV_KEY=$(echo "$KEYGEN_OUT" | grep "private_key:" | head -1 | awk '{print $2}' | tr -d '"')
SHORT_ID=$(echo "$KEYGEN_OUT" | grep "short_ids:" | head -1 | grep -oP '\["[^"]+"\]' | tr -d '[]"')
IMPORT_LINK=$(echo "$KEYGEN_OUT" | grep "^nightveil://" | head -1)

if [ -z "$PRIV_KEY" ]; then
    echo -e "  ${RED}Key generation failed:${NC}"
    echo "$KEYGEN_OUT"
    exit 1
fi

# Write config
cat > "$INSTALL_DIR/server.yaml" << YAML
server:
  listen: "0.0.0.0:$NV_PORT"
$TLS_BLOCK
  auth:
    private_key: "$PRIV_KEY"
    short_ids:
      - "$SHORT_ID"
    max_time_diff: 120
  transport:
    type: "xhttp"
    max_chunk_size: 14336
    session_timeout: 30
    max_parallel_uploads: 4
  middleware:
    - type: "padding"
      min_bytes: 64
      max_bytes: 256
  fallback:
    mode: "default"
YAML

echo -e "  ${GREEN}Config written${NC}"

# Systemd service
cat > "$SERVICE" << SERVICE
[Unit]
Description=Nightveil Server
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/nv server -config $INSTALL_DIR/server.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SERVICE

systemctl daemon-reload
systemctl enable nightveil
systemctl restart nightveil

# Save installer for management
cp "$(readlink -f "$0" 2>/dev/null || echo "$0")" "$INSTALL_DIR/manage.sh" 2>/dev/null || true

# Done
echo ""
echo -e "  ${CYAN}================================${NC}"
echo -e "  ${GREEN} Nightveil installed!${NC}"
echo -e "  ${CYAN}================================${NC}"
echo ""
echo -e "  Server: ${GREEN}$SERVER_IP:$NV_PORT${NC}"
echo ""
echo -e "  ${CYAN}Import link:${NC}"
echo -e "  ${GREEN}$IMPORT_LINK${NC}"
echo ""
echo -e "  ${CYAN}Management:${NC}"
echo "    bash $INSTALL_DIR/manage.sh"
echo ""
echo -e "  ${CYAN}Quick commands:${NC}"
echo "    systemctl status nightveil"
echo "    systemctl restart nightveil"
echo "    journalctl -u nightveil -f"
echo ""
