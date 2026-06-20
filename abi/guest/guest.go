package guest

import "github.com/ethndotsh/switchboard/sdk"

func CurrentRequest() sdk.Request {
	return sdk.CurrentRequest()
}

func Return(action sdk.Action) int32 {
	return sdk.Return(action)
}
