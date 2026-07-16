# Switchboard

Switchboard is an open-source prototype for programmable reverse proxy rules.

Users write request rules in Go, compile them to WebAssembly with TinyGo, upload immutable content-addressed bundles to a registry, and proxy instances poll a channel pointer to hot-swap the active rule without restarting the proxy.

Switchboard keeps the long-lived dataplane stable, moves fast-changing request policy into versioned Wasm guests, and activates versions only after they pass their own declared behavioral tests inside the proxy that will serve them. Deployments are idempotent, recorded as append-only revisions, and reversible with one command.

Rule deployments do not restart the proxy. Bundles are downloaded, verified, compiled, warmed, tested, and activated off the request path, then swapped in atomically. In-flight requests continue on the runtime they started with. If activation fails, the bundle is quarantined and the last known-good version keeps serving — and with a local cache enabled, it keeps serving even across proxy restarts while the registry is down.

## Architecture

![Switchboard architecture](docs/switchboard-arch-diagram.png)

## Shape

- Caddy handler module: `http.handlers.switchboard`
- CLI: `init`, `build`, `test`, `eval`, `replay`, `deploy`, `status`, `history`, `diff`, `promote`, `rollback`, `inspect`, `serve`
- Registries: S3-compatible object storage, local `file://`, read-only `https://`
- Runtime: wazero with enforced execution timeouts and memory limits
- Guest rules: TinyGo WASI modules with a small host-function ABI (`switchboard/v3`)
- Reconciliation: background channel polling with quarantine, backoff, and durable last-known-good fallback

## Install

Run the Caddy image with Switchboard already built in:

```sh
docker pull ghcr.io/ethndotsh/switchboard-caddy:latest
```

Install the CLI onto your `PATH`:

```sh
go install github.com/ethndotsh/switchboard/cmd/switchboard@latest
```

For local development from this repository:

```sh
go install ./cmd/switchboard
```

Install TinyGo too: https://tinygo.org/getting-started/install/.

## Quickstart

No object storage required — the `file://` registry works out of the box:

```sh
mkdir my-rules
cd my-rules
switchboard init --name my-rules --registry file://./registry
```

This writes:

```text
go.mod
switchboard.yaml
rules/basic/rule.go
rules/basic/tests.yaml
```

Rule projects use plain Go packages. `switchboard build` generates the TinyGo/Wasm export wrapper:

```go
package basic

import "github.com/ethndotsh/switchboard/sdk"

func Handle(req sdk.Request) sdk.Action {
	if req.Path() == "/blocked" {
		return sdk.Deny(403).WithReason("blocked-path")
	}

	return sdk.Next().SetRequestHeader("x-powered-by", "switchboard")
}
```

Build, test, and poke at the bundle locally:

```sh
switchboard build      # compiles rules and embeds tests.yaml into the bundle
switchboard test       # runs the bundle against its behavioral test cases
switchboard eval --method GET --path /blocked
```

Deploy it and serve traffic through it:

```sh
switchboard deploy
switchboard serve --listen :8080 --upstream localhost:3000 --registry file://./registry --channel prod
```

`switchboard.yaml` supplies the defaults:

```yaml
name: my-rules
rule: ./rules/basic
dist: ./dist
namespace: customer-a
channel: prod
registry: file://./registry
```

Environment variables are expanded before the YAML is parsed (`$VAR`, `${VAR}`, `${VAR:-fallback}`, `${VAR-fallback}`).

## Local Testing

### `switchboard test`

A bundle can declare its behavioral contract in `tests.yaml` (picked up automatically next to the rule package, or passed with `--cases`):

```yaml
schema: switchboard.tests/v1
cases:
  - name: blocks unauthenticated admin requests
    request:
      method: GET
      host: example.com
      path: /admin
      headers:
        x-user-role: viewer
    expect:
      action: deny
      status: 401
      reason: admin-auth-required

  - name: permits health checks
    request:
      method: GET
      path: /health
    expect:
      action: next
```

`expect` supports partial matching on `action`, `status`, `reason`, `location`, `rewrite_path`, `rewrite_host`, `rewrite_query`, `body_contains`, `metadata`, `request_headers`, and `response_headers` — only the fields you name are asserted. Requests support `method`, `scheme`, `host`, `path`, `query`, `client_ip`, `tls`, `headers`, and `cookies`.

The suite is embedded in the bundle and hashed into its identity. **Every proxy re-runs the suite against the candidate runtime before activation**, so activation means "this exact artifact passed its declared contract under the exact runtime that will serve traffic" — not just "the module compiled."

### `switchboard eval`

Run one request against a built bundle (or a deployed channel) and see the decision:

```sh
switchboard eval ./dist \
  --method GET \
  --host example.com \
  --path /admin \
  -H "x-user-role: viewer"
```

```text
decision:  deny
reason:    admin-auth-required
status:    401
```

`--json` prints the full action.

### `switchboard replay`

Feed captured Caddy JSON access logs through two bundles and diff every decision offline. Because rules have no body access, no external calls, and no mutable state, replay reproduces production decisions exactly:

```sh
switchboard replay ./access.jsonl \
  --current prod \
  --candidate ./dist
```

```text
Requests processed:     12492104
Changed decisions:      8331
New denials:            17
Changed rewrites:       6992
Candidate errors:       0
Candidate p99 exec:     8.1 us
```

`--verbose` streams each difference; `--fail-on-new-denials` and `--fail-on-change` gate CI pipelines. Memory stays bounded regardless of log size.

## Registries

`registry` accepts a URL; the scheme picks the implementation:

| Scheme | Read | Write | Revisions | Notes |
| --- | --- | --- | --- | --- |
| `s3://bucket/prefix` | yes | yes | conditional writes | S3-compatible object storage; credentials from `SWITCHBOARD_S3_*` env |
| `file:///path`, `./path` | yes | yes | `O_EXCL` | local development, tests, `serve`, single machines |
| `https://host/base` | yes | no | generation walk | any static origin (object-store website endpoint, CDN); ETag revalidation |

S3 environment variables:

```sh
SWITCHBOARD_S3_ENDPOINT=localhost:9000
SWITCHBOARD_S3_ACCESS_KEY=minioadmin
SWITCHBOARD_S3_SECRET_KEY=minioadmin
SWITCHBOARD_S3_BUCKET=switchboard
SWITCHBOARD_S3_INSECURE=true
```

Objects use this layout:

```text
channels/prod.json
revisions/prod/0000000041.json
revisions/prod/0000000042.json
bundles/sha256-<digest>/module.wasm
bundles/sha256-<digest>/manifest.json
bundles/sha256-<digest>/tests.yaml
bundles/sha256-<digest>/checksum.txt
bundles/sha256-<digest>/descriptor.json
```

Namespaces isolate tenants: with `namespace: customer-a`, everything nests under `namespaces/customer-a/…`.

The proxy no longer requires the object store at startup: registry construction is config-only, unreachable stores surface as reconciler retries with exponential backoff, and (with `cache_dir`) the last activated bundle keeps serving from local disk.

## Bundle Format

Every bundle carries a `descriptor.json`:

```json
{
  "schema": "switchboard.descriptor/v1",
  "abi": "switchboard/v3",
  "manifest": {"name": "my-rules", "abi": "switchboard/v3", "entrypoint": "handle", "language": "go-tinygo"},
  "artifacts": {
    "module.wasm": {"digest": "sha256:…", "size": 30762},
    "tests.yaml":  {"digest": "sha256:…", "size": 372}
  },
  "provenance": {"built_at": "…", "builder": "switchboard/0.1.0 tinygo/0.41.1", "source_commit": "…"},
  "signatures": []
}
```

The **bundle ID** is `sha256-<hex>` over the canonical JSON of the identity zone (`schema`, `abi`, `manifest`, `artifacts`). Provenance and signatures are annotations that never change the ID, so byte-identical rebuilds deduplicate: re-deploying unchanged content skips the upload entirely. Every registry verifies every artifact digest — and that the stored ID matches the descriptor-derived ID — on both upload and download. The `signatures` field is reserved for future bundle signing.

`checksum.txt` (module-only digest) is still written and verified for compatibility, but `descriptor.json` is authoritative. Legacy bundles without descriptors still load, with a warning.

## Deployment Operations

Every deploy appends an immutable revision record and repoints the channel:

```sh
switchboard status  --channel prod          # pointer + latest revision
switchboard history --channel prod          # GEN  BUNDLE  DEPLOYED_AT  BY  COMMIT  NOTE
switchboard diff    prod ./dist [--tests]   # artifact digests; --tests cross-runs suites
switchboard promote --from staging --to prod
switchboard rollback --channel prod                       # previous different bundle
switchboard rollback --channel prod --to sha256-ab12cd34  # or a specific bundle
```

Revisions record generation, bundle digest, previous generation, deploy time, deployer (`SWITCHBOARD_DEPLOYER`/`GITHUB_ACTOR`/`$USER`), source commit, and CI run. Rollback and promote are roll-forward operations: they append a new generation pointing at older content; history is never rewritten.

Concurrent deployers are serialized by conditional creation of the generation object (`If-None-Match: *` on S3, `O_CREATE|O_EXCL` on file). Stores that reject conditional writes fall back to a stat-then-put emulation with a small race window — MinIO and AWS S3 support conditional writes natively.

## Rule Chains

A bundle is a normal Go package exporting one `Handle(req sdk.Request) sdk.Action`; logic can span files and compose with `sdk.Chain`:

```go
func Handle(req sdk.Request) sdk.Action {
	return sdk.Chain(req,
		BlockInternalPaths,
		RewriteLegacyPaths,
		AddRuleHeader,
	)
}
```

Rules that return `sdk.Next()` accumulate their request patch, response headers, metadata, and reason; later rules observe the request as rewritten so far. The first deny, redirect, or respond short-circuits and carries the accumulated state.

### The SDK surface

Request accessors: `Method`, `Path`, `Host`, `RawQuery`, `Query(name)`, `Scheme`, `Protocol`, `RemoteAddr`, `ClientIP` (adapter-resolved, honors Caddy `trusted_proxies`), `TLS`, `Header(name)`, `HeaderValues(name)`, `Cookie(name)`.

Actions: `Next()`, `Deny(status)`, `Redirect(status, location)`, `Rewrite(path)`, `Respond(status, body)` — plus chainable builders:

- `SetRequestHeader` / `AddRequestHeader` / `DeleteRequestHeader` — mutate the upstream request
- `SetResponseHeader` / `AddResponseHeader` / `DeleteResponseHeader` — mutate the client response
- `RewriteHost` / `RewritePath` / `RewriteQuery`
- `WithReason(string)` — shows up in logs, test output, and replay diffs
- `SetMetadata(name, value)` — exposed as Caddy request variables

Request-header and response-header operations are explicitly separate; the v2 API where one operation meant different things per action is gone.

### Routing on rule metadata

The Caddy adapter maps `SetMetadata` to request variables, so a rule can pick backends without implementing any proxying:

```go
func Handle(req sdk.Request) sdk.Action {
	if bucket(req.Cookie("user_id")) < 10 {
		return sdk.Next().SetMetadata("backend", "v2").WithReason("v2-canary")
	}
	return sdk.Next().SetMetadata("backend", "v1").WithReason("stable")
}
```

```caddyfile
example.com {
	route {
		switchboard {
			registry s3 s3://rules/prod
			channel prod
		}

		@v2 vars backend v2
		reverse_proxy @v2 app-v2:8080
		reverse_proxy app-v1:8080
	}
}
```

Canary routing, tenant shard selection, cache policy, and rate-limit keys all compose the same way: Switchboard emits the decision, native Caddy modules act on it. See [examples/ab-canary-routing](examples/ab-canary-routing).

## Caddy

```caddyfile
:8080 {
	route {
		switchboard {
			registry s3
			namespace customer-a
			channel prod
			poll_interval 2s
			invoke_timeout 50ms
			memory_limit 32mb
			max_action_bytes 64kb
			max_header_ops 32
			max_response_body 8kb
			cache_dir /var/lib/switchboard
			fail_mode last_good
			fallback_fail_mode open
			pool_size 16
			pool_autoscale on
			min_pool_size 16
			max_pool_size 64
		}

		reverse_proxy localhost:9000
	}
}
```

The handler never downloads, compiles, or instantiates bundles on the request path. A background reconciler polls the channel pointer, downloads immutable bundles, verifies every digest, compiles, warms the pool, runs the embedded test suite, and atomically swaps the active runtime.

Decision fields land in access logs (`switchboard_decision`, `switchboard_reason`, `switchboard_bundle_id`) and in the `switchboard.decision` / `switchboard.reason` request variables.

Build with `xcaddy`:

```sh
xcaddy build --with github.com/ethndotsh/switchboard/caddy@latest
```

## Limits & Security

Guest execution is bounded and guest output is validated — a compromised or buggy rule cannot take the proxy down with it:

| Directive | Default | Enforces |
| --- | --- | --- |
| `invoke_timeout` | 50ms | real preemption: the wazero runtime is built with `WithCloseOnContextDone`, so a spinning guest is forcibly stopped at the deadline |
| `memory_limit` | 32mb | hard cap on guest linear memory (wasm pages) |
| `max_action_bytes` | 64kb | running quota over all guest-produced strings (headers, locations, rewrites, metadata, reason) |
| `max_header_ops` | 32 | request + response header operations combined |
| `max_response_body` | 8kb | `Respond` body, checked before reading guest memory |

Validation applied to every guest-produced value: status codes must be 100–599 (300–399 for redirects), header names must be RFC 7230 tokens, header values reject CR/LF/NUL (no header injection), paths and locations reject control characters, rewrite paths must start with `/`. A violating action is **rejected, not clamped** — the invocation errors and flows through your `fail_mode` policy, because silently downgrading a malformed deny into a pass-through would be worse than failing loudly.

Fail modes when the rule is unavailable (no runtime, pool exhausted, trap, timeout, invalid action):

- `open` (default): the request continues to the next handler
- `closed`: 503
- `last_good`: retry the previous runtime once, then apply `fallback_fail_mode` (`open`|`closed`)

## Durable Last-Known-Good

With `cache_dir` set, every successful activation is persisted locally (write, fsync, atomic rename). On startup the proxy verifies and activates the cached bundle **before** touching the registry, then reconciles in the background — a machine that served a known-good bundle five seconds before a restart keeps serving it even if the object store is down. The same directory hosts wazero's compilation cache, so restarts skip recompiling unchanged modules.

A bundle that fails compilation, validation, or its embedded tests is **quarantined**: it is not retried until the channel pointer changes, so one broken deployment cannot cause a compile storm across your fleet. Transient registry failures use exponential backoff (1s doubling to 5m, with jitter).

## Standalone Mode

Try Switchboard without Caddy — one binary, no object store:

```sh
switchboard serve \
  --listen :8080 \
  --upstream localhost:3000 \
  --registry file://./registry \
  --channel dev
```

`serve` exposes `GET /switchboard/status` (reconciler + pool state as JSON) and `GET /metrics` (Prometheus) — on the main listener by default, or a separate one with `--status-listen :9090`.

Embed the same middleware in any Go HTTP server:

```go
service, err := engine.Start(ctx, engine.Config{RegistryURL: "file://./registry", Channel: "dev"}, logger)
handler := httpadapter.Middleware(service, httpadapter.Options{FailMode: "open", Logger: logger})
server := &http.Server{Handler: handler(application)}
```

## Observability

- `Service.Status()` / `GET /switchboard/status`: active and last-good bundle IDs, reconciler state (desired vs active bundle, quarantine, backoff, last test report, activation counters), pool stats (target size, instances, in-flight, exhaustions).
- Prometheus metrics: `switchboard_invocations_total{decision,result}`, `switchboard_invocation_duration_seconds`, `switchboard_pool_instances`, `switchboard_pool_inflight`, `switchboard_pool_exhaustions_total`, `switchboard_activation_total{result}`. Labels are deliberately low-cardinality; keep tenant IDs, paths, and user IDs in logs.
- Caddy access logs carry `switchboard_decision`, `switchboard_reason`, and `switchboard_bundle_id` per request; Caddy's own Prometheus/OTLP middleware metrics cover latency and sizes.

## Docker

Published images:

```text
ghcr.io/ethndotsh/switchboard-caddy:latest
ghcr.io/ethndotsh/switchboard-caddy:v0.1.0
ghcr.io/ethndotsh/switchboard-caddy:sha-<commit>
```

Build a Caddy image with the Switchboard module from a checkout:

```sh
docker build -t switchboard-caddy .
```

## Performance Notes

Switchboard optimizes for predictable request-path behavior. Bundles are reconciled, compiled, warmed, and tested off-path; request handling borrows an already-warmed Wasm instance from the active runtime pool. The ABI reads request fields lazily and writes action patches through host calls, so "continue unchanged" pays only for what it touches.

Local in-process benchmarks on an Apple M4 Pro with CLI-built optimized artifacts:

| Path | Approximate Result | What It Measures |
| --- | ---: | --- |
| HTTP request conversion | 0 allocs/op | Adapter conversion into a Switchboard request |
| Warm simple block rule | 2.8 us/op | Borrow pooled instance, read path, emit deny |
| Warm one-header next rule | 7.1 us/op | Read path and emit one validated header patch |
| Warm known-header read rule | 7.4 us/op | Read a named request header and emit deny |
| Warm multi-header patch rule | 11.3 us/op | Emit set, add, add, and delete header ops |
| Parallel warm-pool invoke | 1.5 us/op | Concurrent borrows from a warmed pool |

Guest output validation and the richer v3 request surface cost roughly 1–3 us over the unvalidated v2 path — the price of CRLF checks, token validation, and output quotas on every emitted value.

## Examples

Real-world rules, each with a behavioral test suite:

- [examples/admin-gate](examples/admin-gate) — cookie/role auth gate with reasons
- [examples/legacy-redirects](examples/legacy-redirects) — path map to 301 redirects
- [examples/security-headers](examples/security-headers) — response security headers, HSTS only on TLS
- [examples/maintenance-mode](examples/maintenance-mode) — synthetic 503 page via `Respond`
- [examples/ab-canary-routing](examples/ab-canary-routing) — deterministic canary via metadata → Caddy `vars` routing
- [examples/basic](examples/basic), [examples/chained](examples/chained) — the smallest possible rules

## Prior Art

Switchboard borrows architectural lessons from Railway's Hikari CDN writeup: keep the host dataplane stable, move request policy into versioned guests, reconcile toward desired state, validate candidates off-path, and activate with an atomic swap. See [Railway's Hikari CDN architecture](https://blog.railway.com/p/railway-cdn).

The Wasm runtime path also borrows lessons from Arcjet's production wazero writeups: precompile modules, avoid request-path instantiation, and prefer deliberate data-shape changes over fragile parser tricks. See [Lessons from running WebAssembly in production with Go & wazero](https://blog.arcjet.com/lessons-from-running-webassembly-in-production-with-go-wazero/) and [Making Arcjet's Wasm bot detector smaller and faster](https://blog.arcjet.com/making-arcjets-wasm-bot-detector-smaller-and-faster/).

## Roadmap

Planned for v0.2.0:

- **Shadow candidate evaluation** — mirror a sample of live requests against a candidate channel off the request path and report decision mismatches before promoting.
- **Parameterized modules and immutable data bundles** — channel-scoped config values and read-only data sets (redirect maps, CIDR sets) that swap atomically with the code, without rebuilding Wasm.
- **OCI registry support** — bundles as OCI artifacts (digest = bundle, tag = channel) for GHCR/ECR and Cosign.
- **Bundle signing** — Ed25519/Sigstore verification over the descriptor (the `signatures` field is already reserved).
- **Caddy admin API module** — `GET /switchboard/status` through Caddy's admin endpoint.

## Limitations

- Request body and response body **filtering** are intentionally out of scope (small synthetic response bodies via `Respond` are supported).
- Rules cannot make outbound network calls, by design: deterministic latency, safe replay, and simple failure behavior depend on it.
- There is no hosted control plane.
- Caddy is the reference adapter.
- Switchboard is not a CDN, cache, BGP system, anycast network, or hosted control plane.
