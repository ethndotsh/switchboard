package guest

import (
	"encoding/json"
	"unsafe"

	"github.com/ethndotsh/switchboard/sdk"
)

func CurrentRequest() sdk.Request {
	length := requestLen()
	buf := make([]byte, length)
	if length > 0 {
		readRequest(uint32(uintptr(unsafe.Pointer(&buf[0]))))
	}
	var req sdk.Request
	_ = json.Unmarshal(buf, &req)
	return req
}

func Return(action sdk.Action) int32 {
	data, err := json.Marshal(action)
	if err != nil {
		return 1
	}
	if len(data) == 0 {
		writeAction(0, 0)
		return 0
	}
	writeAction(uint32(uintptr(unsafe.Pointer(&data[0]))), uint32(len(data)))
	return 0
}
