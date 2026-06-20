#!/bin/sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$ROOT"

export SWITCHBOARD_S3_ENDPOINT=localhost:9000
export SWITCHBOARD_S3_ACCESS_KEY=switchboard
export SWITCHBOARD_S3_SECRET_KEY=switchboard-secret
export SWITCHBOARD_S3_BUCKET=switchboard-test
export SWITCHBOARD_S3_INSECURE=true
export GOCACHE="${GOCACHE:-/tmp/switchboard-go-cache}"

echo "==> starting e2e stack"
if [ "${SWITCHBOARD_E2E_REBUILD:-false}" = "true" ] || ! docker image inspect e2e-caddy:latest >/dev/null 2>&1; then
  docker compose -f e2e/docker-compose.yml build caddy
fi
docker compose -f e2e/docker-compose.yml down >/dev/null 2>&1 || true
docker compose -f e2e/docker-compose.yml up -d

echo "==> building switchboard CLI"
go build -o /tmp/switchboard-e2e ./cmd/switchboard

wait_http() {
  url="$1"
  for _ in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "timed out waiting for $url" >&2
  return 1
}

assert_contains() {
  text="$1"
  needle="$2"
  label="$3"
  if ! printf "%s" "$text" | grep -F "$needle" >/dev/null; then
    echo "assertion failed: $label" >&2
    echo "$text" >&2
    exit 1
  fi
}

assert_status() {
  path="$1"
  want="$2"
  got="$(curl -sS -o /tmp/switchboard-e2e-response -w "%{http_code}" "http://localhost:8080$path")"
  if [ "$got" != "$want" ]; then
    echo "expected $path status $want, got $got" >&2
    cat /tmp/switchboard-e2e-response >&2 || true
    exit 1
  fi
}

wait_rule() {
  want="$1"
  for _ in $(seq 1 30); do
    body="$(curl -fsS http://localhost:8080/)"
    if printf "%s" "$body" | grep -F "\"rule\": \"$want\"" >/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "timed out waiting for rule $want" >&2
  docker compose -f e2e/docker-compose.yml logs caddy >&2
  exit 1
}

wait_http http://localhost:8080/

echo "==> initial traffic succeeds before rules"
body="$(curl -fsS http://localhost:8080/)"
assert_contains "$body" '"rule": ""' "initial request should have no active rule"

echo "==> deploying rule v1"
/tmp/switchboard-e2e build --skip-tidy --name e2e-v1 --out /tmp/switchboard-dist-v1 ./e2e/rules/v1
/tmp/switchboard-e2e deploy /tmp/switchboard-dist-v1 --channel prod
wait_rule v1
assert_status /blocked 403

echo "==> checking traffic during v2 deploy"
(
  end=$(( $(date +%s) + 8 ))
  while [ "$(date +%s)" -lt "$end" ]; do
    curl -fsS http://localhost:8080/ >/dev/null
  done
) &
traffic_pid="$!"
/tmp/switchboard-e2e build --skip-tidy --name e2e-v2 --out /tmp/switchboard-dist-v2 ./e2e/rules/v2
/tmp/switchboard-e2e deploy /tmp/switchboard-dist-v2 --channel prod
wait "$traffic_pid"
wait_rule v2
assert_status /blocked 451

echo "==> deploying invalid bundle and confirming last good remains active"
mkdir -p /tmp/switchboard-dist-bad
printf "not wasm" > /tmp/switchboard-dist-bad/module.wasm
checksum="sha256:$(shasum -a 256 /tmp/switchboard-dist-bad/module.wasm | awk '{print $1}')"
cat > /tmp/switchboard-dist-bad/manifest.json <<EOF
{
  "name": "bad",
  "version": "bad-$(date +%s)",
  "abi_version": "switchboard/v1",
  "entrypoint": "handle",
  "language": "go-tinygo"
}
EOF
printf "%s\n" "$checksum" > /tmp/switchboard-dist-bad/checksum.txt
/tmp/switchboard-e2e deploy /tmp/switchboard-dist-bad --channel prod
sleep 3
wait_rule v2
assert_status /blocked 451

echo "==> confirming namespaces isolate same channel name"
/tmp/switchboard-e2e deploy /tmp/switchboard-dist-v1 --namespace customer-a --channel shared
/tmp/switchboard-e2e deploy /tmp/switchboard-dist-v2 --namespace customer-b --channel shared
inspect_a="$(/tmp/switchboard-e2e inspect --namespace customer-a --channel shared)"
inspect_b="$(/tmp/switchboard-e2e inspect --namespace customer-b --channel shared)"
assert_contains "$inspect_a" '"namespace": "customer-a"' "namespace customer-a pointer"
assert_contains "$inspect_b" '"namespace": "customer-b"' "namespace customer-b pointer"
if [ "$inspect_a" = "$inspect_b" ]; then
  echo "expected namespaced pointers to differ" >&2
  exit 1
fi

echo "==> e2e passed"
