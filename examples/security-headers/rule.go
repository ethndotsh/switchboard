// Package securityheaders stamps baseline security headers onto every
// response: clickjacking protection, MIME-sniffing protection, a strict
// referrer policy, and HSTS on TLS connections.
package securityheaders

import "github.com/ethndotsh/switchboard/sdk"

func Handle(req sdk.Request) sdk.Action {
	action := sdk.Next().
		SetResponseHeader("x-frame-options", "DENY").
		SetResponseHeader("x-content-type-options", "nosniff").
		SetResponseHeader("referrer-policy", "strict-origin-when-cross-origin")
	if req.TLS() {
		// Only meaningful (and safe) on HTTPS: never advertise HSTS over
		// plaintext.
		action = action.SetResponseHeader("strict-transport-security", "max-age=31536000; includeSubDomains")
	}
	return action
}
