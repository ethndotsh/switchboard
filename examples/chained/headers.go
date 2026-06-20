package main

import "github.com/ethndotsh/switchboard/sdk"

func AddRuleHeader(req sdk.Request) sdk.Action {
	req.Headers["x-switchboard-rule"] = []string{"chained"}
	return sdk.Next(req)
}
