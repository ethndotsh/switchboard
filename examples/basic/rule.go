package main

import (
	"github.com/ethndotsh/switchboard/abi/guest"
	"github.com/ethndotsh/switchboard/sdk"
)

func Handle(req sdk.Request) sdk.Action {
	if req.Path() == "/blocked" {
		return sdk.Deny(403)
	}
	if req.Path() == "/old" {
		return sdk.Redirect(302, "/new")
	}
	if req.Header("x-switchboard-deny") == "yes" {
		return sdk.Deny(418)
	}
	if req.Path() == "/headers" {
		return sdk.Next().
			SetRequestHeader("x-switchboard-rule", "basic").
			AddRequestHeader("x-switchboard-list", "one").
			AddRequestHeader("x-switchboard-list", "two").
			DeleteRequestHeader("x-switchboard-delete")
	}
	return sdk.Next().SetRequestHeader("x-switchboard-rule", "basic")
}

//export handle
func handle() int32 {
	return guest.Return(Handle(guest.CurrentRequest()))
}

func main() {}
