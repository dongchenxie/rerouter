#!/usr/bin/env bash
set -euo pipefail

export B_BASE_URL=${B_BASE_URL:-"https://pkbest.ru/"}
export CACHE_TTL_SECONDS=${CACHE_TTL_SECONDS:-3600}
export CACHE_PATTERNS=${CACHE_PATTERNS:-"/sitemap.xml,/blog/*,/products/*"}
export REDIRECT_STATUS=${REDIRECT_STATUS:-302}

echo "Starting dev stack with B_BASE_URL=${B_BASE_URL}"
docker compose -f docker-compose.dev.yml up --build

