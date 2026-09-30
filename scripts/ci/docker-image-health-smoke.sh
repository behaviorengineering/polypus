#!/usr/bin/env bash
# Publish-ready smoke: pull images, verify binary load (glibc), then HTTP /health in running containers.
set -euo pipefail

GW_IMAGE="${1:?gateway image ref}"
SY_IMAGE="${2:?switchyard image ref}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SMOKE_ROUTES="${ROOT}/scripts/ci/switchyard-smoke-routes.toml"
WAIT_SECS="${DOCKER_HEALTH_WAIT_SECS:-90}"

if [[ ! -f "$SMOKE_ROUTES" ]]; then
  echo "missing smoke routes: $SMOKE_ROUTES" >&2
  exit 1
fi

echo "docker-image-health-smoke: pull ${GW_IMAGE} ${SY_IMAGE}"
docker pull "$GW_IMAGE"
docker pull "$SY_IMAGE"

echo "docker-image-health-smoke: binary load (switchyard-server --help)"
docker run --rm --entrypoint switchyard-server "$SY_IMAGE" --help >/dev/null

echo "docker-image-health-smoke: binary load (polypus --help)"
docker run --rm --entrypoint polypus "$GW_IMAGE" --help >/dev/null

wait_container_health() {
  local cid="$1"
  local url="$2"
  local i=0
  while [ "$i" -lt "$WAIT_SECS" ]; do
    if docker exec "$cid" curl -sf "$url" >/dev/null 2>&1; then
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  echo "docker-image-health-smoke: timeout waiting for $url (container $cid)" >&2
  docker logs "$cid" --tail 80 >&2 || true
  return 1
}

SY_CID=""
GW_CID=""
cleanup() {
  [[ -n "$SY_CID" ]] && docker rm -f "$SY_CID" >/dev/null 2>&1 || true
  [[ -n "$GW_CID" ]] && docker rm -f "$GW_CID" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "docker-image-health-smoke: switchyard /health"
SY_CID="$(docker run -d \
  -v "${SMOKE_ROUTES}:/etc/switchyard/smoke-routes.toml:ro" \
  -e SWITCHYARD_CONFIG=/etc/switchyard/smoke-routes.toml \
  "$SY_IMAGE")"
wait_container_health "$SY_CID" "http://127.0.0.1:4000/health"

echo "docker-image-health-smoke: gateway /health"
GW_CID="$(docker run -d \
  -e POLYPUS_OTEL=0 \
  -e POLYPUS_SWITCHYARD=0 \
  "$GW_IMAGE")"
wait_container_health "$GW_CID" "http://127.0.0.1:1320/health"

echo "docker-image-health-smoke: ok"
