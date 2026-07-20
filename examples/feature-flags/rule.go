// Package featureflags drives request policy from a typed config file bundled
// with the rule. data/flags.json is decoded once into a struct via
// sdk.DataJSON; changing behavior (flip maintenance, block a path, change the
// banner) is a data-only edit that still ships as a validated, versioned
// bundle with the same rollback guarantees as a code change.
package featureflags

import "github.com/ethndotsh/switchboard/sdk"

type flags struct {
	Maintenance  bool     `json:"maintenance"`
	Banner       string   `json:"banner"`
	BlockedPaths []string `json:"blocked_paths"`
}

func Handle(req sdk.Request) sdk.Action {
	var f flags
	if err := sdk.DataJSON("flags.json", &f); err != nil {
		return sdk.Deny(500).WithReason("bad-flags")
	}
	if f.Maintenance {
		return sdk.Respond(503, "down for maintenance").WithReason("maintenance")
	}
	for _, path := range f.BlockedPaths {
		if req.Path() == path {
			return sdk.Deny(403).WithReason("blocked-path")
		}
	}
	action := sdk.Next().WithReason("ok")
	if f.Banner != "" {
		action = action.SetResponseHeader("x-banner", f.Banner)
	}
	return action
}
