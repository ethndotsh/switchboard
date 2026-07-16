// Package maintenancemode short-circuits requests with a static 503 page
// while the origin is being worked on. Operators flip the switch by having
// an upstream layer set the x-maintenance header (or by routing traffic to
// a path that is permanently down).
package maintenancemode

import "github.com/ethndotsh/switchboard/sdk"

const maintenancePage = "<html><body><h1>Be right back</h1></body></html>"

func Handle(req sdk.Request) sdk.Action {
	if req.Header("x-maintenance") == "on" || req.Path() == "/always-down" {
		return sdk.Respond(503, maintenancePage).
			WithReason("maintenance").
			SetResponseHeader("Retry-After", "300").
			SetResponseHeader("Content-Type", "text/html; charset=utf-8")
	}
	return sdk.Next()
}
