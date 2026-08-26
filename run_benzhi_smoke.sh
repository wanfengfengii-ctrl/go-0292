#!/usr/bin/env bash
# Smoke test for the UHPC wet-joint traffic-release service.
#
# Builds the server, starts it against a temporary data directory, drives a real
# public API flow (health, recipe, span, joint creation), and tears everything
# down. Deterministic, no external network, and never calls `go test`.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PORT="${SMOKE_PORT:-18080}"
BASE="http://127.0.0.1:${PORT}"

# Cleanup removes the server process and the temporary data directory (which
# also holds the built binary).
cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  if [[ -n "${DATA_DIR:-}" ]]; then
    rm -rf "${DATA_DIR}"
  fi
}
trap cleanup EXIT

DATA_DIR="$(mktemp -d)"
BIN="${DATA_DIR}/uhpc-server"

echo "Building server..."
(
  cd "${HERE}"
  go build -o "${BIN}" ./cmd/server
)

echo "Starting server on :${PORT} (data=${DATA_DIR})"
ADDR=":${PORT}" DATA_PATH="${DATA_DIR}/state.db" "${BIN}" &
SERVER_PID=$!

# Wait for the health endpoint to become ready (with a bounded retry loop).
ready=""
for _ in $(seq 1 50); do
  resp="$(curl -sS -o /dev/null -w '%{http_code}' "${BASE}/v1/health" 2>/dev/null || true)"
  if [[ "${resp}" == "200" ]]; then
    ready="1"
    break
  fi
  sleep 0.1
done
if [[ -z "${ready}" ]]; then
  echo "server did not become healthy" >&2
  exit 1
fi

# Assert health body reports "ok" (capture response, do not pipe curl to grep).
health_body="$(curl -sS "${BASE}/v1/health")"
if ! grep -q '"status":"ok"' <<<"${health_body}"; then
  echo "unexpected health response: ${health_body}" >&2
  exit 1
fi

# Register a recipe and a span, then create a locked joint.
recipe_resp="$(curl -sS -X POST "${BASE}/v1/recipes" \
  -H 'Content-Type: application/json' \
  -d '{"name":"UHPC-1","allow_deviation":{"raw":2,"scale":2},"flow_min":{"raw":180,"scale":1},"flow_max":{"raw":260,"scale":1},"work_window":100,"min_strength":{"raw":50,"scale":0},"min_bond_strength":{"raw":2,"scale":0},"max_shrinkage":{"raw":500,"scale":0}}')"
if ! grep -q '"name":"UHPC-1"' <<<"${recipe_resp}"; then
  echo "recipe creation failed: ${recipe_resp}" >&2
  exit 1
fi

span_resp="$(curl -sS -X POST "${BASE}/v1/spans" \
  -H 'Content-Type: application/json' \
  -d '{"id":"S1","coordinate_scale":1000,"allowed_recipes":["UHPC-1"],"rule_digest":"v1"}')"
if ! grep -q '"id":"S1"' <<<"${span_resp}"; then
  echo "span creation failed: ${span_resp}" >&2
  exit 1
fi

joint_resp="$(curl -sS -X POST "${BASE}/v1/joints" \
  -H 'Content-Type: application/json' \
  -d '{"joint_number":"J1","span_id":"S1","recipe":"UHPC-1","lock_version":1,"geometry":{"range":{"start":0,"end":999},"segments":[{"index":0,"start":0,"end":499},{"index":1,"start":500,"end":999}],"layers":2,"direction":"ASCENDING","rebar":{"cover":50,"lap":0},"shear_keys":[]},"surface_zones":[{"id":"Z1","required":true}],"mix_plans":[{"batch":"B1","sequence":0,"powder":100000,"water":20000,"admixture":1000,"fiber":5000}],"curing":{"duration_minutes":60,"min_temperature":{"raw":20,"scale":0},"min_humidity":{"raw":90,"scale":0}},"adjacency":[[1],[0]],"segment_zones":{"0":["Z1"],"1":["Z1"]}}')"
if ! grep -q '"joint_number":"J1"' <<<"${joint_resp}"; then
  echo "joint creation failed: ${joint_resp}" >&2
  exit 1
fi

echo "smoke test passed"
