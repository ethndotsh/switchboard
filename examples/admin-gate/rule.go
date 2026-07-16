// Package admingate protects /admin/* behind a role cookie. Unauthenticated
// visitors (no session cookie) are sent to the login page; authenticated
// visitors without the admin role are denied outright.
package admingate

import (
	"strings"

	"github.com/ethndotsh/switchboard/sdk"
)

func Handle(req sdk.Request) sdk.Action {
	if !strings.HasPrefix(req.Path(), "/admin/") && req.Path() != "/admin" {
		return sdk.Next()
	}
	if req.Cookie("session") == "" {
		return sdk.Redirect(302, "/login").WithReason("login-required")
	}
	if req.Cookie("role") != "admin" {
		return sdk.Deny(403).WithReason("admin-auth-required")
	}
	return sdk.Next()
}
