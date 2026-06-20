//go:build tinygo || wasip1 || wasm

package guest

//go:wasmimport switchboard request_len
func requestLen() uint32

//go:wasmimport switchboard read_request
func readRequest(ptr uint32)

//go:wasmimport switchboard write_action
func writeAction(ptr uint32, length uint32)
