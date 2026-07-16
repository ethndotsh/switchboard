# admin-gate

A cookie-based authorization gate for an admin area. Any request under `/admin` is checked against two cookies: `session` (proves the visitor is logged in at all) and `role` (proves they are allowed into the admin area). Visitors without a session are redirected to the login page so they can authenticate; visitors who are logged in but lack the `admin` role are denied with a 403. Everything outside `/admin` passes through untouched, so the rule can sit in front of an entire site.

## Behavior

| Request | Result |
| --- | --- |
| `/admin*` with `session` and `role=admin` cookies | `next` |
| `/admin*` without a `session` cookie | `redirect 302 /login`, reason `login-required` |
| `/admin*` with a `session` cookie but `role != admin` | `deny 403`, reason `admin-auth-required` |
| Any other path | `next` |

## Build, test, deploy

```sh
switchboard build ./examples/admin-gate --out ./dist-example
switchboard test ./dist-example
```

The build embeds `tests.yaml` (picked up automatically from the rule directory, or pass `--cases examples/admin-gate/tests.yaml` explicitly), so the compiled artifact carries its behavioral contract with it. Point your proxy config at the built bundle in `./dist-example` to deploy.
