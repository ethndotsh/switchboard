# Switchboard End-to-End Workspace

This workspace runs a complete local Switchboard stack:

- MinIO as S3-compatible object storage
- A custom Caddy binary with the Switchboard module
- A backend service that echoes request path and received headers
- Rule v1, rule v2, and a deliberately invalid bundle

Run it from the repository root:

```sh
sh e2e/scripts/run.sh
```

The script verifies:

1. Caddy serves traffic before any rule is active.
2. Deploying rule v1 changes behavior without restarting Caddy.
3. Requests keep succeeding while rule v2 is deployed.
4. Rule v2 becomes active without restarting Caddy.
5. A bad bundle does not replace the last known-good runtime.

Useful manual commands:

```sh
docker compose -f e2e/docker-compose.yml up -d --build
docker compose -f e2e/docker-compose.yml logs -f caddy
curl -i http://localhost:8080/
curl -i http://localhost:8080/blocked
docker compose -f e2e/docker-compose.yml down -v
```

The e2e scripts use local MinIO credentials only:

```sh
SWITCHBOARD_S3_ENDPOINT=localhost:9000
SWITCHBOARD_S3_ACCESS_KEY=switchboard
SWITCHBOARD_S3_SECRET_KEY=switchboard-secret
SWITCHBOARD_S3_BUCKET=switchboard-test
SWITCHBOARD_S3_INSECURE=true
```
