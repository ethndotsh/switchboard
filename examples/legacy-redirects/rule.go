// Package legacyredirects permanently redirects retired URLs to their
// modern replacements so old links, bookmarks, and search results keep
// working after a site restructure.
package legacyredirects

import "github.com/ethndotsh/switchboard/sdk"

var redirects = map[string]string{
	"/old-pricing":    "/pricing",
	"/blog.php":       "/blog",
	"/docs/v1":        "/docs",
	"/about-us.html":  "/about",
	"/support/portal": "/help",
}

func Handle(req sdk.Request) sdk.Action {
	if target, ok := redirects[req.Path()]; ok {
		return sdk.Redirect(301, target).WithReason("legacy-redirect")
	}
	return sdk.Next()
}
