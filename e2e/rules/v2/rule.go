package main

import (
	"github.com/ethndotsh/switchboard/abi/guest"
	"github.com/ethndotsh/switchboard/sdk"
)

func Handle(req sdk.Request) sdk.Action {
	if req.Path == "/blocked" {
		return sdk.Deny(451)
	}
	req.Headers["x-switchboard-rule"] = []string{"v2"}
	return sdk.Next(req)
}

//export handle
func handle() int32 {
	return guest.Return(Handle(guest.CurrentRequest()))
}

func main() {}
