# ip-allowlist

Deny every request whose client IP is not on an allowlist that ships *inside*
the bundle. The list lives in [`data/allowlist.txt`](data/allowlist.txt), a
read-only data file embedded at build time. Because data files are hashed into
the bundle's identity, the allowlist is versioned, validated, and rolled back
exactly like the rule code: changing an entry produces a new immutable bundle
that must pass its embedded tests before it can activate.

The rule reads the file through `sdk.DataSet`, which parses the newline-delimited
list into a membership set once and caches it for the life of the instance, so
the per-request cost is a single map lookup.

## Behavior

| Request client IP | Result |
| --- | --- |
| present in `allowlist.txt` | `next`, reason `allowlisted` |
| absent (or empty) | `deny 403`, reason `not-allowlisted` |

The rule matches whole IP strings. The client IP is adapter-resolved: under
Caddy it honors `trusted_proxies`, so put the allowlist rule behind correct
proxy configuration if you terminate TLS upstream.

## Data files

`data/allowlist.txt` is picked up automatically from the `data/` directory next
to the rule. Point elsewhere with `switchboard build --data ./path`, and cap the
embedded size with `--max-data-bytes` (default 4mb). See the
[data files guide](../../docs/content/guides/data-files.mdx).

## Build, test, deploy

```sh
switchboard build ./examples/ip-allowlist --out ./dist-example
switchboard test ./dist-example
```

The build embeds both `tests.yaml` and `data/allowlist.txt`; the test suite runs
against the exact bundled data, so "tests passed" means "passed with this
allowlist."
