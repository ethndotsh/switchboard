package main

import (
	"github.com/ethndotsh/switchboard/abi/guest"
	"github.com/ethndotsh/switchboard/sdk"
)

func Handle(req sdk.Request) sdk.Action {
	if req.Path() == "/blocked" {
		return sdk.Deny(403)
	}
	return sdk.Next().SetRequestHeader("x-switchboard-rule", "v1")
}

//export handle
func handle() int32 {
	return guest.Return(Handle(guest.CurrentRequest()))
}

func main() {}
