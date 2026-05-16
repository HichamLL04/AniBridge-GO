#!/bin/sh
set -e

CONFIG_FILE="/config/config.yml"

# Auto-initialize config from example on first run
if [ ! -f "$CONFIG_FILE" ]; then
  echo "==> No config found at $CONFIG_FILE, creating from example..."
  cp /app/config.example.yml "$CONFIG_FILE"
  echo "==> Config initialized! Edit /config/config.yml to customize."
fi

exec /app/anibridge-go --config "$CONFIG_FILE" "$@"
