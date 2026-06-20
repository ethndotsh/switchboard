package main

import "github.com/ethndotsh/switchboard/sdk"

func RewriteLegacyPaths(req sdk.Request) sdk.Action {
	if req.Path == "/old" {
		return sdk.Rewrite(req, "/new")
	}
	return sdk.Next(req)
}
