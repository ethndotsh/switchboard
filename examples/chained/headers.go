package main

import "github.com/ethndotsh/switchboard/sdk"

func AddRuleHeader(req sdk.Request) sdk.Action {
	return sdk.Next().SetHeader("x-switchboard-rule", "chained")
}
