// Package abcanaryrouting deterministically routes a fixed slice of users
// to a canary backend. The user_id cookie is hashed with FNV-1a; users whose
// hash lands in the bottom 10% of buckets are tagged for the v2 backend,
// everyone else (including anonymous visitors) stays on stable v1. The tag
// is exposed as metadata so the proxy config decides where each pool goes.
package abcanaryrouting

import "github.com/ethndotsh/switchboard/sdk"

// canaryPercent is the share of identified users routed to v2 (0-100).
const canaryPercent = 10

func fnv1a(s string) uint32 {
	hash := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		hash ^= uint32(s[i])
		hash *= 16777619
	}
	return hash
}

func Handle(req sdk.Request) sdk.Action {
	userID := req.Cookie("user_id")
	if userID != "" && fnv1a(userID)%100 < canaryPercent {
		return sdk.Next().SetMetadata("backend", "v2").WithReason("v2-canary")
	}
	return sdk.Next().SetMetadata("backend", "v1").WithReason("stable")
}
