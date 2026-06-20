package main

import "github.com/ethndotsh/switchboard/sdk"

func RewriteLegacyPaths(req sdk.Request) sdk.Action {
	if req.Path() == "/old" {
		return sdk.Rewrite("/new")
	}
	return sdk.Next()
}
