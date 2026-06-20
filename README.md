# Switchboard

Switchboard is an open-source prototype for programmable reverse proxy rules.

Users write request rules in Go, compile them to WebAssembly with TinyGo, upload immutable bundles to object storage, and proxy instances poll a channel pointer to hot-swap the active rule without restarting the proxy.

Switchboard keeps the long-lived dataplane stable, moves fast-changing request policy into versioned Wasm guests, and activates versions only after validation. It applies that pattern to ordinary reverse proxy deployments instead of requiring a custom CDN or edge network.

Rule deployments do not restart the proxy. Bundles are downloaded, verified, compiled, warmed, and validated off the request path, then activated with an atomic swap. In-flight requests continue using the runtime version they started with. If activation fails, the last known-good version remains active.

## Shape

- Caddy handler module: `http.handlers.switchboard`
- CLI: `switchboard init`, `switchboard build`, `switchboard dist`, `switchboard deploy`, `switchboard inspect`
- Registry: S3-compatible object storage only for deploy/inspect/load
- Runtime: wazero
- Guest rules: TinyGo WASI modules with a small host-function ABI
- Reconciliation: background channel polling with last-known-good fallback

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

Create a rule project:

```sh
mkdir my-rules
cd my-rules
switchboard init --name my-rules --registry s3://switchboard/prod
```

This writes:

```text
go.mod
switchboard.yaml
rules/basic/rule.go
```

Rule projects use plain Go packages. `switchboard build` generates the TinyGo/Wasm export wrapper:

```go
package basic

import "github.com/ethndotsh/switchboard/sdk"

func Handle(req sdk.Request) sdk.Action {
	if req.Path == "/blocked" {
		return sdk.Deny(403)
	}

	req.Headers["x-powered-by"] = []string{"switchboard"}
	return sdk.Next(req)
}
```

Build a distributable bundle:

```sh
switchboard build
```

or equivalently:

```sh
switchboard dist
```

`build` runs `go mod tidy` before invoking TinyGo so the generated SDK import is resolved. Use `--skip-tidy` in locked-down CI after dependencies are already pinned.

Rule builds default to a speed-oriented TinyGo profile:

```text
-opt=2 -panic=trap -no-debug
```

Use `--tinygo-opt`, `--tinygo-panic print`, or `--tinygo-debug` when you want a more debug-friendly artifact.

Use `--wasm-opt` to run Binaryen's `wasm-opt` after TinyGo. In local testing this mainly reduced bundle size, so it is opt-in instead of part of the default latency path.

Deploy the bundle:

```sh
switchboard deploy
```

Inspect the active channel:

```sh
switchboard inspect
```

`switchboard.yaml` supplies the defaults:

```yaml
name: my-rules
rule: ./rules/basic
dist: ./dist
namespace: customer-a
channel: prod
registry: s3://switchboard/prod
```

Environment variables are expanded before the YAML is parsed:

```yaml
name: ${SWITCHBOARD_NAME:-my-rules}
rule: ${SWITCHBOARD_RULE:-./rules/basic}
dist: ${SWITCHBOARD_DIST:-./dist}
namespace: ${SWITCHBOARD_NAMESPACE:-customer-a}
channel: ${SWITCHBOARD_CHANNEL:-prod}
registry: s3://${SWITCHBOARD_S3_BUCKET}/${SWITCHBOARD_S3_PREFIX:-prod}
```

Supported forms are `$VAR`, `${VAR}`, `${VAR:-fallback}` for unset or empty values, and `${VAR-fallback}` for unset values only.

## Object Storage

Switchboard expects an S3-compatible registry. For local development, run MinIO or use any S3-compatible endpoint.

Required environment variables:

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
bundles/2026-06-19T12-00-00Z-abc123/module.wasm
bundles/2026-06-19T12-00-00Z-abc123/manifest.json
bundles/2026-06-19T12-00-00Z-abc123/checksum.txt
```

Namespaces are optional. Without a namespace, Switchboard keeps the global layout above. With `namespace: customer-a`, channels and bundles are isolated under:

```text
namespaces/customer-a/channels/prod.json
namespaces/customer-a/bundles/2026-06-19T12-00-00Z-abc123/module.wasm
namespaces/customer-a/bundles/2026-06-19T12-00-00Z-abc123/manifest.json
namespaces/customer-a/bundles/2026-06-19T12-00-00Z-abc123/checksum.txt
```

Registry URL prefixes remain a base path. `registry: s3://switchboard/acme` plus `namespace: edge` writes under `acme/namespaces/edge/...`.

## Repository Example

Build a rule:

```sh
go run ./cmd/switchboard build --out ./dist ./examples/basic
```

Deploy it to object storage:

```sh
go run ./cmd/switchboard deploy ./dist --channel prod
```

Inspect the active channel pointer:

```sh
go run ./cmd/switchboard inspect --channel prod
```

## Rule Chains

A Switchboard bundle is a normal Go package. It needs one `Handle(req sdk.Request) sdk.Action` function, but the rule logic can live across as many files as you want:

```text
rules/public/
  rule.go
  security.go
  routing.go
  headers.go
```

`rule.go` is the top-level ordering file:

```go
func Handle(req sdk.Request) sdk.Action {
	return sdk.Chain(req,
		BlockInternalPaths,
		RewriteLegacyPaths,
		AddRuleHeader,
	)
}
```

The other files define ordinary Go functions:

```go
func BlockInternalPaths(req sdk.Request) sdk.Action {
	if req.Path == "/internal" {
		return sdk.Deny(404)
	}
	return sdk.Next(req)
}
```

Build the whole package:

```sh
switchboard build ./rules/public
```

To use different rule packages for different Caddy routes, deploy them to different channels and attach the channel where that route lives:

```caddyfile
example.com {
	route /admin/* {
		switchboard {
			registry s3
			namespace customer-a
			channel admin
		}

		reverse_proxy admin:3000
	}

	route {
		switchboard {
			registry s3
			namespace customer-a
			channel public
		}

		reverse_proxy app:3000
	}
}
```

Each route gets one atomic active bundle. Namespace groups channels; channel remains the stable deployment pointer. Inside a bundle, ordering is explicit Go code.

## Caddy

Use the published Caddy image:

```sh
docker run --rm -p 8080:8080 \
	-v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" \
	-e SWITCHBOARD_S3_ENDPOINT \
	-e SWITCHBOARD_S3_ACCESS_KEY \
	-e SWITCHBOARD_S3_SECRET_KEY \
	-e SWITCHBOARD_S3_BUCKET \
	ghcr.io/ethndotsh/switchboard-caddy:latest
```

Build with `xcaddy`:

```sh
xcaddy build --with github.com/ethndotsh/switchboard/caddy@latest
```

For local development from this repository:

```sh
xcaddy build --with github.com/ethndotsh/switchboard/caddy=./caddy
```

Caddyfile:

```caddyfile
:8080 {
	route {
		switchboard {
			registry s3
			namespace customer-a
			channel prod
			poll_interval 2s
			pool_size 16
			pool_autoscale on
			min_pool_size 16
			max_pool_size 64
			fail_mode open
		}

		reverse_proxy localhost:9000
	}
}
```

The handler never downloads, compiles, or instantiates bundles on the request path. A background reconciler polls `channels/{channel}.json`, downloads immutable bundles, verifies checksums, compiles Wasm, warms the configured minimum guest pool, validates the candidate, and atomically swaps the active runtime.

If the warmed pool is exhausted on the request path, Switchboard treats the rule as unavailable and applies `fail_mode`.

Warm pools adapt by default. `pool_size` remains the default floor, `min_pool_size` can set that floor explicitly, and `max_pool_size` bounds background growth. Use `pool_autoscale off` for fixed-capacity pools.

## Docker

Published images are available at:

```text
ghcr.io/ethndotsh/switchboard-caddy:latest
ghcr.io/ethndotsh/switchboard-caddy:v0.0.2
ghcr.io/ethndotsh/switchboard-caddy:sha-<commit>
```

Build a Caddy image with the Switchboard module from a checkout:

```sh
docker build -t switchboard-caddy .
```

Pin the public Go module version by overriding `SWITCHBOARD_VERSION`:

```sh
docker build \
	--build-arg SWITCHBOARD_REPLACE= \
	--build-arg SWITCHBOARD_VERSION=latest \
	-t switchboard-caddy .
```

The default Docker build uses the checkout copied into the image build context. The pinned public-module mode clears `SWITCHBOARD_REPLACE`, then resolves `github.com/ethndotsh/switchboard` through Go modules.

GitHub Actions publishes the Caddy image from the checked-out source commit on pushes to the default branch and version tags. Pull requests build the image without publishing it.

## Performance Notes

Switchboard optimizes for predictable request-path behavior rather than zero-cost rule execution. Bundles are reconciled, compiled, warmed, and validated off-path; request handling borrows an already-warmed Wasm instance from the active runtime pool.

Local in-process benchmarks on an Apple M4 Pro with CLI-built optimized artifacts:

| Path | Approximate Result | What It Measures |
| --- | ---: | --- |
| HTTP request conversion | 0 allocs/op | Adapter conversion into a Switchboard request |
| Warm simple block rule | 6.9 us/op | Borrow pooled instance, invoke rule, decode deny action |
| Warm header-mutating rule | 29-30 us/op | Current JSON request/action path with multiple headers |

The hot path does not download, compile, or instantiate Wasm. The remaining cost is mostly rule execution plus ABI serialization. Header-heavy rules are slower today because `switchboard/v0` sends the full request as JSON and returns a JSON action. The next ABI iterations are aimed at making "continue unchanged" and "set one header" proportional to the actual change instead of the full header set.

`wasm-opt` is optional because local testing showed it mainly reduced bundle size. It did not materially change warmed request latency.

Docker e2e HTTP numbers are useful only as a directional smoke test. Sequential local pass-through landed in the same millisecond range as stock Caddy proxying to the same Python backend, but production latency depends on host, pool sizing, traffic shape, rule complexity, TinyGo flags, and memory pressure.

## Prior Art

Switchboard borrows architectural lessons from Railway's Hikari CDN writeup: keep the host dataplane stable, move request policy into versioned guests, reconcile toward desired state, validate candidates off-path, and activate with an atomic swap. See [Railway's Hikari CDN architecture](https://blog.railway.com/p/railway-cdn).

The Wasm runtime path also borrows lessons from Arcjet's production wazero writeups: precompile modules, avoid request-path instantiation, treat `wasm-opt` as a measured build-time tradeoff, and prefer deliberate data-shape changes over fragile parser tricks. See [Lessons from running WebAssembly in production with Go & wazero](https://blog.arcjet.com/lessons-from-running-webassembly-in-production-with-go-wazero/) and [Making Arcjet's Wasm bot detector smaller and faster](https://blog.arcjet.com/making-arcjets-wasm-bot-detector-smaller-and-faster/).

## TinyGo

Install TinyGo from https://tinygo.org/getting-started/install/.

The build command shells out to:

```sh
tinygo build -target=wasi -opt=2 -panic=trap -no-debug -o dist/module.wasm ./examples/basic
```

## Limitations

- Request body and response body mutation are intentionally out of scope.
- There is no hosted control plane.
- Caddy is the reference adapter.
- The ABI is intentionally small and will likely change.
- Registry operations require S3-compatible object storage credentials.
- Switchboard is not a CDN, cache, BGP system, anycast network, or hosted control plane.
