#!/bin/sh
set -e

# Ensure cache directory exists and is owned by app
APP_UID=10001
APP_GID=10001

mkdir -p /app/cache
# If bind-mounted from host with root ownership, fix it here
chown -R ${APP_UID}:${APP_GID} /app/cache || true

# Ensure logs directory exists and is writable by app
mkdir -p /app/logs
chown -R ${APP_UID}:${APP_GID} /app/logs || true

# Drop privileges and run the provided command (or default CMD)
exec su-exec ${APP_UID}:${APP_GID} "$@"
