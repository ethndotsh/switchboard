# maintenance-mode

A maintenance switch that serves a small static "Be right back" page instead of proxying to the origin. When the request carries the header `x-maintenance: on` (simulating an operator toggle set by an upstream layer or edge config), or when the path is `/always-down`, the rule responds directly with a `503 Service Unavailable`, an inline HTML body, a `Retry-After: 300` hint for clients and crawlers, and the correct `Content-Type`. All other traffic passes through untouched, so the rule is safe to leave permanently in the chain and flip on only during deploys or incidents.

## Behavior

| Request | Result |
| --- | --- |
| Header `x-maintenance: on` (any path) | `respond 503` with HTML body, reason `maintenance`, `Retry-After: 300`, `Content-Type: text/html; charset=utf-8` |
| Path `/always-down` | same 503 maintenance response |
| Anything else | `next` |

## Build, test, deploy

```sh
switchboard build ./examples/maintenance-mode --out ./dist-example
switchboard test ./dist-example
```

The build embeds `tests.yaml` from the rule directory so the artifact carries its contract. Point your proxy config at the built bundle in `./dist-example` to deploy.
