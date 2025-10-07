#!/usr/bin/env bash
set -euo pipefail

IMAGE_NAME=${IMAGE_NAME:-463052811/go-rerouter}
IMAGE_TAG=${IMAGE_TAG:-latest}
DOCKERFILE=${DOCKERFILE:-Dockerfile.release}
CONTEXT_DIR=${CONTEXT_DIR:-.}

if [[ -z "${IMAGE_NAME}" ]]; then
  echo "[error] IMAGE_NAME must be provided (e.g. export IMAGE_NAME=youruser/rerouter)" >&2
  exit 1
fi

echo "[info] Building ${IMAGE_NAME}:${IMAGE_TAG} using ${DOCKERFILE}"
docker build -f "${DOCKERFILE}" -t "${IMAGE_NAME}:${IMAGE_TAG}" "${CONTEXT_DIR}"

echo "[info] Pushing ${IMAGE_NAME}:${IMAGE_TAG}"
docker push "${IMAGE_NAME}:${IMAGE_TAG}"

echo "[success] Image pushed: ${IMAGE_NAME}:${IMAGE_TAG}"
