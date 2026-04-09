#!/bin/sh
# Nightveil Docker Entrypoint
# Auto-initializes on first run, then starts server.
#
# Environment variables:
#   NV_PORT  — listen port (default: 443)
#   NV_NAME  — server display name (default: Nightveil)
#   NV_DEST  — REALITY dest (e.g. google.com:443). If set, enables REALITY mode.

CONFIG="/etc/nightveil/server.yaml"

if [ ! -f "$CONFIG" ]; then
    echo ""
    echo "  ========================================="
    echo "   Nightveil — First Run Initialization"
    echo "  ========================================="
    echo ""

    PORT="${NV_PORT:-443}"
    NAME="${NV_NAME:-Nightveil}"
    DEST="${NV_DEST:-}"

    nv init -port "$PORT" -name "$NAME" -dir /etc/nightveil

    # If REALITY dest specified, add to config
    if [ -n "$DEST" ]; then
        # Insert dest line after tls: section
        sed -i "/cert_file/a\\    dest: \"$DEST\"" "$CONFIG" 2>/dev/null || true
        echo ""
        echo "  REALITY mode: probes forwarded to $DEST"
    fi

    echo ""
    echo "  ================================================="
    echo "  Import link printed above — send it to your users"
    echo "  To see it again:  docker compose logs nightveil"
    echo "  To add users:     docker compose exec nightveil nv keygen ..."
    echo "  ================================================="
    echo ""
fi

exec nv server -config "$CONFIG"
