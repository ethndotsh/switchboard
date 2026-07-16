# ab-canary-routing

Deterministic canary routing: send a fixed 10% slice of identified users to a new `v2` backend while everyone else stays on stable `v1`. The rule hashes the `user_id` cookie with FNV-1a (implemented inline, no imports beyond the SDK) and takes the hash mod 100 — users landing in buckets 0-9 are tagged `backend=v2`, everyone else (including anonymous visitors with no cookie) is tagged `backend=v1`. Because the hash is deterministic, each user sees the same backend on every request — no sticky sessions or state required — and widening the rollout is a one-constant change. The rule never picks the upstream itself; it exposes the decision as metadata (a Caddy request variable) and the proxy config routes on it.

## Behavior

| Request | Result |
| --- | --- |
| `user_id` cookie present, `fnv1a(user_id) % 100 < 10` | `next`, metadata `backend=v2`, reason `v2-canary` |
| `user_id` cookie present, bucket >= 10 | `next`, metadata `backend=v1`, reason `stable` |
| No `user_id` cookie | `next`, metadata `backend=v1`, reason `stable` |

## Companion Caddyfile

The metadata surfaces as a Caddy variable named `backend`, so routing to the two pools is:

```caddyfile
example.com {
	route {
		switchboard {
			registry s3
			namespace my-app
			channel prod
		}

		@v2 vars backend v2
		reverse_proxy @v2 v2-backend:8080

		reverse_proxy v1-backend:8080
	}
}
```

Requests the rule tags with `backend=v2` match the `@v2` matcher and are proxied to the canary pool; everything else falls through to the stable pool.

## Build, test, deploy

```sh
switchboard build ./examples/ab-canary-routing --out ./dist-example
switchboard test ./dist-example
```

The build embeds `tests.yaml` from the rule directory so the artifact carries its contract, including cases pinned to concrete cookie values on both sides of the hash split.
