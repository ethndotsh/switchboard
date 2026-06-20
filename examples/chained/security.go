package main

import "github.com/ethndotsh/switchboard/sdk"

func BlockInternalPaths(req sdk.Request) sdk.Action {
	if req.Path == "/internal" {
		return sdk.Deny(404)
	}
	return sdk.Next(req)
}
