# Changelog

All notable changes to this project are documented in this file. The format
is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - 2026-07-16

### Breaking

- **ABI v3.** The guest ABI is now `switchboard/v3`; bundles built with earlier
  SDKs are rejected at activation with a rebuild hint. Rebuild with the current
  SDK: `SetHeader`/`AddHeader`/`DeleteHeader` are replaced by explicit
  `SetRequestHeader`/`SetResponseHeader` (and Add/Delete variants) — the old
  API silently meant *request* headers on next/rewrite but *response* headers
  on deny/redirect.
- **Content-addressed bundle IDs.** Bundle IDs are now `sha256-<digest>` derived
  from a canonical bundle descriptor instead of timestamp+random. Existing
  channels keep working; newly built bundles get the new IDs.
- `sdk.NewRequest` now takes a single `sdk.RequestData` struct.

### Added

- **Real execution limits.** The wazero runtime is built with
  `WithCloseOnContextDone`, so `invoke_timeout` (default 50ms) forcibly
  preempts spinning guests. New operator caps: `memory_limit` (32mb),
  `max_action_bytes` (64kb), `max_header_ops` (32), `max_response_body` (8kb).
- **Guest output validation.** Status-code ranges, RFC 7230 header-name tokens,
  CRLF/NUL rejection, control-character checks on paths/locations, and output
  quotas. Invalid actions are rejected (not clamped) and flow through
  `fail_mode`.
- **Richer request/action ABI.** Requests expose `Host`, `RawQuery`,
  `Query(name)`, `Scheme`, `Protocol`, `RemoteAddr`, adapter-resolved
  `ClientIP`, `TLS`, and `Cookie(name)`. Actions gain `Respond(status, body)`,
  split request/response header operations, `RewriteHost/Path/Query`,
  `WithReason`, and `SetMetadata`.
- **Caddy variables integration.** Rule metadata becomes Caddy request
  variables (`@v2 vars backend v2` matchers work), enabling backend selection,
  canary routing, and shard selection from rules. Decisions, reasons, and
  bundle IDs are appended to access logs.
- **`switchboard test` + in-bundle behavioral suites.** `tests.yaml` cases are
  embedded in the bundle, hashed into its identity, and re-run inside every
  proxy before activation.
- **`switchboard eval`** — run one request against a bundle locally and print
  the decision.
- **`switchboard replay`** — stream captured Caddy JSON access logs through two
  bundles and diff every decision offline (changed decisions, new/lifted
  denials, changed rewrites, candidate errors, p50/p99), with CI gates.
- **Canonical bundle descriptor.** `descriptor.json` covers module, manifest
  identity, and test-suite digests plus build provenance; deploys of identical
  content skip the upload. A `signatures` field is reserved for future signing.
- **Deployment operations.** Append-only revision history per channel with
  conditional writes against concurrent deployers, and new commands:
  `status`, `history`, `diff` (with `--tests` cross-runs), `promote`,
  `rollback`, `version`.
- **Registries.** `registry.Open` URL factory with new `file://` (development,
  tests, single machines) and read-only `https://` (ETag revalidation)
  implementations alongside S3.
- **Durable last-known-good.** `cache_dir` persists every activated bundle
  (fsync + atomic rename); on restart the proxy verifies and serves the cached
  bundle before the registry is reachable. The same directory hosts wazero's
  compilation cache.
- **Activation quarantine.** Permanently broken bundles (bad manifest, digest
  mismatch, compile/validation/test failure) are quarantined until the channel
  pointer changes — no more compile storms; transient registry errors use
  exponential backoff with jitter.
- **`fail_mode last_good`.** On a trap, timeout, or invalid action, retry the
  previous runtime once, then apply `fallback_fail_mode` (open|closed).
- **Runtime retirement.** Replaced runtimes are closed after in-flight
  invocations drain (15s grace), fixing a leak where every rollout stranded a
  warm instance pool.
- **Standalone mode.** `switchboard serve` — a single-binary reverse proxy with
  live rule reloads — plus an embeddable `net/http` middleware
  (`httpadapter.Middleware`).
- **Observability.** `Service.Status()`, `GET /switchboard/status`, and
  Prometheus metrics (`switchboard_invocations_total`,
  `switchboard_invocation_duration_seconds`, pool gauges,
  `switchboard_activation_total`).
- **Examples.** Five real-world rules with test suites: admin-gate,
  legacy-redirects, security-headers, maintenance-mode, ab-canary-routing.

### Changed

- Proxy startup no longer requires the object store: the `BucketExists`
  preflight is gone (the CLI still pings for fast feedback), and registry
  construction is config-only.
- The channel pointer carries `generation` and `descriptor_digest`.
- Unhealthy pool instances are replaced synchronously, so fixed-size pools no
  longer drain permanently under repeated guest errors.
- `switchboard init` scaffolds a `tests.yaml` alongside the rule.

### Deprecated

- `checksum.txt` (still written and verified; `descriptor.json` is
  authoritative).
- Bundles without descriptors still load, with a warning.

## [0.0.3] - 2026-06-20

- ABI v2, wazero request-path optimizations, adaptive pool sizing, artifact
  build pipeline, published Caddy images.

## [0.0.2] - 2026-06-19

- Namespaces, e2e stack, initial GHCR image publishing.

## [0.0.1] - 2026-06-19

- Initial prototype: Caddy handler, S3 registry, TinyGo SDK, reconciler with
  last-known-good retention.
