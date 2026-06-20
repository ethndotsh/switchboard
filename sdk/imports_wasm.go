//go:build tinygo || wasip1 || wasm

package sdk

import "unsafe"

//go:wasmimport switchboard request_method_len
func requestMethodLen() uint32

//go:wasmimport switchboard read_request_method
func readRequestMethod(ptr uint32)

//go:wasmimport switchboard request_path_len
func requestPathLen() uint32

//go:wasmimport switchboard read_request_path
func readRequestPath(ptr uint32)

//go:wasmimport switchboard request_header_value_count
func requestHeaderValueCount(namePtr uint32, nameLen uint32) uint32

//go:wasmimport switchboard request_header_value_len
func requestHeaderValueLen(namePtr uint32, nameLen uint32, index uint32) uint32

//go:wasmimport switchboard read_request_header_value
func readRequestHeaderValue(namePtr uint32, nameLen uint32, index uint32, valuePtr uint32)

//go:wasmimport switchboard action_next
func actionNext()

//go:wasmimport switchboard action_deny
func actionDeny(status int32)

//go:wasmimport switchboard action_redirect
func rawActionRedirect(status int32, locationPtr uint32, locationLen uint32)

//go:wasmimport switchboard action_rewrite
func rawActionRewrite(pathPtr uint32, pathLen uint32)

//go:wasmimport switchboard action_header_set
func rawActionHeaderSet(namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32)

//go:wasmimport switchboard action_header_add
func rawActionHeaderAdd(namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32)

//go:wasmimport switchboard action_header_delete
func rawActionHeaderDelete(namePtr uint32, nameLen uint32)

func requestMethod() string {
	length := requestMethodLen()
	buf := make([]byte, length)
	if length > 0 {
		readRequestMethod(uint32(uintptr(unsafe.Pointer(&buf[0]))))
	}
	return string(buf)
}

func requestPath() string {
	length := requestPathLen()
	buf := make([]byte, length)
	if length > 0 {
		readRequestPath(uint32(uintptr(unsafe.Pointer(&buf[0]))))
	}
	return string(buf)
}

func requestHeaderValues(name string) []string {
	namePtr, nameLen := stringPtr(name)
	count := requestHeaderValueCount(namePtr, nameLen)
	values := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		length := requestHeaderValueLen(namePtr, nameLen, i)
		buf := make([]byte, length)
		if length > 0 {
			readRequestHeaderValue(namePtr, nameLen, i, uint32(uintptr(unsafe.Pointer(&buf[0]))))
		}
		values = append(values, string(buf))
	}
	return values
}

func actionRedirect(status int, location string) {
	ptr, length := stringPtr(location)
	rawActionRedirect(int32(status), ptr, length)
}

func actionRewrite(path string) {
	ptr, length := stringPtr(path)
	rawActionRewrite(ptr, length)
}

func actionHeaderSet(name, value string) {
	namePtr, nameLen := stringPtr(name)
	valuePtr, valueLen := stringPtr(value)
	rawActionHeaderSet(namePtr, nameLen, valuePtr, valueLen)
}

func actionHeaderAdd(name, value string) {
	namePtr, nameLen := stringPtr(name)
	valuePtr, valueLen := stringPtr(value)
	rawActionHeaderAdd(namePtr, nameLen, valuePtr, valueLen)
}

func actionHeaderDelete(name string) {
	namePtr, nameLen := stringPtr(name)
	rawActionHeaderDelete(namePtr, nameLen)
}

func stringPtr(value string) (uint32, uint32) {
	if len(value) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.StringData(value)))), uint32(len(value))
}
