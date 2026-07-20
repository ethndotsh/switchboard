// Package ipallowlist denies any request whose client IP is not in an
// allowlist bundled with the rule. The allowlist lives in data/allowlist.txt,
// a read-only data file baked into the bundle and hashed into its identity, so
// changing the list is a normal build-and-deploy with the same validation and
// rollback as a code change. The set is parsed once and cached for the life of
// the instance.
package ipallowlist

import "github.com/ethndotsh/switchboard/sdk"

func Handle(req sdk.Request) sdk.Action {
	if sdk.DataSet("allowlist.txt").Contains(req.ClientIP()) {
		return sdk.Next().WithReason("allowlisted")
	}
	return sdk.Deny(403).WithReason("not-allowlisted")
}
