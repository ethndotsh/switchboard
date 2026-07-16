# legacy-redirects

A permanent-redirect table for URLs that no longer exist after a site restructure. The rule keeps a simple `map[string]string` of retired paths to their modern replacements and answers matching requests with a `301 Moved Permanently`, so old bookmarks, inbound links, and search-engine results keep working (and search engines transfer ranking to the new URLs). Any path not in the table passes through to the origin untouched — adding a new redirect is a one-line change to the map.

## Behavior

| Request | Result |
| --- | --- |
| `/old-pricing` | `redirect 301 /pricing`, reason `legacy-redirect` |
| `/blog.php` | `redirect 301 /blog`, reason `legacy-redirect` |
| `/docs/v1` | `redirect 301 /docs`, reason `legacy-redirect` |
| `/about-us.html` | `redirect 301 /about`, reason `legacy-redirect` |
| `/support/portal` | `redirect 301 /help`, reason `legacy-redirect` |
| Anything else | `next` |

## Build, test, deploy

```sh
switchboard build ./examples/legacy-redirects --out ./dist-example
switchboard test ./dist-example
```

The build embeds `tests.yaml` from the rule directory so the artifact carries its contract. Point your proxy config at the built bundle in `./dist-example` to deploy.
