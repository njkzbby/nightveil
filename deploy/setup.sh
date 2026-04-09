#!/bin/bash
# Nightveil server quick setup
# Usage: curl -sL <url> | bash
# Or: scp this file to VPS and run

set -e

echo "=== Nightveil Server Setup ==="

# Install docker if not present
if ! command -v docker &>/dev/null; then
    echo "Installing Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
fi

# Create directory
mkdir -p /opt/nightveil
cd /opt/nightveil

echo "Nightveil directory: /opt/nightveil"
echo ""
echo "Place these files here:"
echo "  docker-compose.yml"
echo "  deploy/server.yaml"
echo "  deploy/cert.pem"
echo "  deploy/key.pem"
echo ""
echo "Then run: docker compose up -d"
echo ""
echo "To update: docker compose build --no-cache && docker compose up -d"
echo "To check logs: docker compose logs -f"
echo "To stop: docker compose down"
