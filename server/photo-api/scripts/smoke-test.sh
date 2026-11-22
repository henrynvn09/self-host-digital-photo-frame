#!/usr/bin/env bash
set -euo pipefail

IMAGE="${1:-photo-api:smoke}"
CONTAINER="photo-api-smoke-$$"
VOLUME="photo-api-smoke-data-$$"
PORT=12283

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PHOTO_API_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "Building $IMAGE..."
docker build -t "$IMAGE" "$PHOTO_API_DIR"

echo "Preparing non-root (1026:100) data volume..."
docker volume create "$VOLUME" >/dev/null
docker run --rm -v "$VOLUME:/data" debian:bookworm-slim chown -R 1026:100 /data

echo "Starting $CONTAINER as UID 1026:100 with read-only /photos:ro..."
docker run -d --name "$CONTAINER" --user 1026:100 \
  -v "$PHOTO_API_DIR:/photos:ro" \
  -v "$VOLUME:/data" \
  -e API_KEY=smoke-secret \
  -p "${PORT}:2283" \
  "$IMAGE" >/dev/null

for _ in $(seq 1 15); do
  if curl -fsS "http://127.0.0.1:${PORT}/api/server/ping" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "Checking /api/server/ping..."
curl -fsS "http://127.0.0.1:${PORT}/api/server/ping" | grep -q '"pong"'

echo "Checking /api/server/version with x-api-key..."
curl -fsS -H "x-api-key: smoke-secret" "http://127.0.0.1:${PORT}/api/server/version" | grep -q '"major"'

echo "Checking heif-convert & GeoNames cities1000.txt inside container..."
docker exec "$CONTAINER" sh -c 'test "$(id -u)" = "1026" && command -v heif-convert >/dev/null && test -s /app/geonames/cities1000.txt'

echo "Smoke test PASSED!"
