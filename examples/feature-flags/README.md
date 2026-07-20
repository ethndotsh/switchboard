# feature-flags

Config-driven request policy from a typed data file. [`data/flags.json`](data/flags.json)
is decoded once into a Go struct with `sdk.DataJSON`; the rule reads no
hardcoded constants:

```json
{
  "maintenance": false,
  "banner": "beta program open",
  "blocked_paths": ["/admin", "/internal"]
}
```

Flipping `maintenance`, adding a path to `blocked_paths`, or changing the banner
is a data-only edit. It still produces a new immutable bundle that must pass its
embedded tests before activating, and rolls back like any other deploy — so
"change a flag" gets the same safety as "change the code."

## Behavior

| Flag state | Result |
| --- | --- |
| `maintenance: true` | `respond 503`, reason `maintenance` |
| request path in `blocked_paths` | `deny 403`, reason `blocked-path` |
| otherwise | `next`, `x-banner` response header set from `banner` |

## Data files

This example shows the typed-config pattern; see the
[data files guide](../../docs/content/guides/data-files.mdx) for every accessor
(`DataJSON`, `DataTOML`, `DataCSV`, `DataJSONL`, `DataSet`, `DataLines`). For a
membership-list example, see [`ip-allowlist`](../ip-allowlist/).

## Build, test, deploy

```sh
switchboard build ./examples/feature-flags --out ./dist-example
switchboard test ./dist-example
```

The build embeds `tests.yaml` and `data/flags.json`; the suite runs against the
exact bundled config, so "tests passed" means "passed with these flags."
