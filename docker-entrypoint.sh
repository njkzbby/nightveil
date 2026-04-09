#!/bin/sh
# Nightveil Docker Entrypoint

CONFIG="/etc/nightveil/server.yaml"

# If running a specific command (setup, adduser, etc.) — pass through
if [ "$1" = "nv" ]; then
    exec "$@"
fi

# Default: start server
if [ ! -f "$CONFIG" ]; then
    echo ""
    echo "  Server not configured yet."
    echo ""
    echo "  Run setup first:"
    echo "    docker compose run --rm nightveil nv setup"
    echo ""
    exit 1
fi

exec nv server -config "$CONFIG"
