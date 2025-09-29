#!/usr/bin/env bash
set -euo pipefail

# Build the production image and extract the static binary to ./dist

IMAGE_NAME=${IMAGE_NAME:-a-site:binary}

echo "[build-native] Building Docker image ${IMAGE_NAME}..."
docker build -t ${IMAGE_NAME} -f Dockerfile .

cid=$(docker create ${IMAGE_NAME})
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT

mkdir -p dist
echo "[build-native] Extracting binary to ./dist/a-site"
docker cp "$cid":/app/a-site ./dist/a-site
docker rm -f "$cid" >/dev/null 2>&1 || true
trap - EXIT

chmod +x ./dist/a-site
echo "[build-native] Done. Run it with: \n  B_BASE_URL=https://your-b-site.example.com ./dist/a-site"

