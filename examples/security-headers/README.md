# security-headers

A response-hardening rule that stamps a baseline set of security headers onto every response passing through the proxy, so individual backends do not each have to remember them. Every request continues to the origin (`next`); the rule only attaches response header operations: `X-Frame-Options: DENY` against clickjacking, `X-Content-Type-Options: nosniff` against MIME sniffing, and a `strict-origin-when-cross-origin` referrer policy. `Strict-Transport-Security` is added only when the request arrived over TLS, since advertising HSTS on plaintext connections is meaningless and unsafe.

## Behavior

| Request | Result |
| --- | --- |
| Any request | `next` + `x-frame-options: DENY`, `x-content-type-options: nosniff`, `referrer-policy: strict-origin-when-cross-origin` |
| ...additionally, if TLS | + `strict-transport-security: max-age=31536000; includeSubDomains` |

## Build, test, deploy

```sh
switchboard build ./examples/security-headers --out ./dist-example
switchboard test ./dist-example
```

The build embeds `tests.yaml` from the rule directory so the artifact carries its contract. Point your proxy config at the built bundle in `./dist-example` to deploy.
