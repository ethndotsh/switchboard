package main

import (
	"github.com/ethndotsh/switchboard/abi/guest"
	"github.com/ethndotsh/switchboard/sdk"
)

func Handle(req sdk.Request) sdk.Action {
	return sdk.Chain(req,
		BlockInternalPaths,
		RewriteLegacyPaths,
		AddRuleHeader,
	)
}

//export handle
func handle() int32 {
	return guest.Return(Handle(guest.CurrentRequest()))
}

func main() {}
