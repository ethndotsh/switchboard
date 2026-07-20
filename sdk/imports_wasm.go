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

//go:wasmimport switchboard request_host_len
func requestHostLen() uint32

//go:wasmimport switchboard read_request_host
func readRequestHost(ptr uint32)

//go:wasmimport switchboard request_raw_query_len
func requestRawQueryLen() uint32

//go:wasmimport switchboard read_request_raw_query
func readRequestRawQuery(ptr uint32)

//go:wasmimport switchboard request_scheme_len
func requestSchemeLen() uint32

//go:wasmimport switchboard read_request_scheme
func readRequestScheme(ptr uint32)

//go:wasmimport switchboard request_protocol_len
func requestProtocolLen() uint32

//go:wasmimport switchboard read_request_protocol
func readRequestProtocol(ptr uint32)

//go:wasmimport switchboard request_remote_addr_len
func requestRemoteAddrLen() uint32

//go:wasmimport switchboard read_request_remote_addr
func readRequestRemoteAddr(ptr uint32)

//go:wasmimport switchboard request_client_ip_len
func requestClientIPLen() uint32

//go:wasmimport switchboard read_request_client_ip
func readRequestClientIP(ptr uint32)

//go:wasmimport switchboard request_tls
func requestTLSRaw() uint32

//go:wasmimport switchboard request_header_value_count
func requestHeaderValueCount(namePtr uint32, nameLen uint32) uint32

//go:wasmimport switchboard request_header_value_len
func requestHeaderValueLen(namePtr uint32, nameLen uint32, index uint32) uint32

//go:wasmimport switchboard read_request_header_value
func readRequestHeaderValue(namePtr uint32, nameLen uint32, index uint32, valuePtr uint32)

//go:wasmimport switchboard request_query_value_len
func requestQueryValueLen(namePtr uint32, nameLen uint32) uint32

//go:wasmimport switchboard read_request_query_value
func readRequestQueryValue(namePtr uint32, nameLen uint32, valuePtr uint32)

//go:wasmimport switchboard request_cookie_len
func requestCookieLen(namePtr uint32, nameLen uint32) uint32

//go:wasmimport switchboard read_request_cookie
func readRequestCookie(namePtr uint32, nameLen uint32, valuePtr uint32)

//go:wasmimport switchboard data_read_len
func dataReadLen(namePtr uint32, nameLen uint32) uint32

//go:wasmimport switchboard read_data
func readData(namePtr uint32, nameLen uint32, valuePtr uint32)

//go:wasmimport switchboard action_next
func actionNext()

//go:wasmimport switchboard action_deny
func actionDeny(status int32)

//go:wasmimport switchboard action_redirect
func rawActionRedirect(status int32, locationPtr uint32, locationLen uint32)

//go:wasmimport switchboard action_rewrite
func actionRewrite()

//go:wasmimport switchboard action_respond
func rawActionRespond(status int32, bodyPtr uint32, bodyLen uint32)

//go:wasmimport switchboard action_rewrite_host
func rawActionRewriteHost(ptr uint32, length uint32)

//go:wasmimport switchboard action_rewrite_path
func rawActionRewritePath(ptr uint32, length uint32)

//go:wasmimport switchboard action_rewrite_query
func rawActionRewriteQuery(ptr uint32, length uint32)

//go:wasmimport switchboard action_req_header_set
func rawActionReqHeaderSet(namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32)

//go:wasmimport switchboard action_req_header_add
func rawActionReqHeaderAdd(namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32)

//go:wasmimport switchboard action_req_header_delete
func rawActionReqHeaderDelete(namePtr uint32, nameLen uint32)

//go:wasmimport switchboard action_resp_header_set
func rawActionRespHeaderSet(namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32)

//go:wasmimport switchboard action_resp_header_add
func rawActionRespHeaderAdd(namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32)

//go:wasmimport switchboard action_resp_header_delete
func rawActionRespHeaderDelete(namePtr uint32, nameLen uint32)

//go:wasmimport switchboard action_set_metadata
func rawActionSetMetadata(keyPtr uint32, keyLen uint32, valuePtr uint32, valueLen uint32)

//go:wasmimport switchboard action_set_reason
func rawActionSetReason(ptr uint32, length uint32)

// Wasm-imported functions cannot be used as values, so callers pass closures.
func readPulled(lenFn func() uint32, readFn func(ptr uint32)) string {
	length := lenFn()
	if length == 0 {
		return ""
	}
	buf := make([]byte, length)
	readFn(uint32(uintptr(unsafe.Pointer(&buf[0]))))
	return string(buf)
}

func requestMethod() string {
	return readPulled(func() uint32 { return requestMethodLen() }, func(ptr uint32) { readRequestMethod(ptr) })
}

func requestPath() string {
	return readPulled(func() uint32 { return requestPathLen() }, func(ptr uint32) { readRequestPath(ptr) })
}

func requestHost() string {
	return readPulled(func() uint32 { return requestHostLen() }, func(ptr uint32) { readRequestHost(ptr) })
}

func requestRawQuery() string {
	return readPulled(func() uint32 { return requestRawQueryLen() }, func(ptr uint32) { readRequestRawQuery(ptr) })
}

func requestScheme() string {
	return readPulled(func() uint32 { return requestSchemeLen() }, func(ptr uint32) { readRequestScheme(ptr) })
}

func requestProtocol() string {
	return readPulled(func() uint32 { return requestProtocolLen() }, func(ptr uint32) { readRequestProtocol(ptr) })
}

func requestRemoteAddr() string {
	return readPulled(func() uint32 { return requestRemoteAddrLen() }, func(ptr uint32) { readRequestRemoteAddr(ptr) })
}

func requestClientIP() string {
	return readPulled(func() uint32 { return requestClientIPLen() }, func(ptr uint32) { readRequestClientIP(ptr) })
}

func requestTLS() bool {
	return requestTLSRaw() != 0
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

func requestQueryValue(name string) string {
	namePtr, nameLen := stringPtr(name)
	length := requestQueryValueLen(namePtr, nameLen)
	if length == 0 {
		return ""
	}
	buf := make([]byte, length)
	readRequestQueryValue(namePtr, nameLen, uint32(uintptr(unsafe.Pointer(&buf[0]))))
	return string(buf)
}

func requestCookie(name string) string {
	namePtr, nameLen := stringPtr(name)
	length := requestCookieLen(namePtr, nameLen)
	if length == 0 {
		return ""
	}
	buf := make([]byte, length)
	readRequestCookie(namePtr, nameLen, uint32(uintptr(unsafe.Pointer(&buf[0]))))
	return string(buf)
}

func dataRead(name string) []byte {
	namePtr, nameLen := stringPtr(name)
	length := dataReadLen(namePtr, nameLen)
	if length == 0 {
		return nil
	}
	buf := make([]byte, length)
	readData(namePtr, nameLen, uint32(uintptr(unsafe.Pointer(&buf[0]))))
	return buf
}

func actionRedirect(status int, location string) {
	ptr, length := stringPtr(location)
	rawActionRedirect(int32(status), ptr, length)
}

func actionRespond(status int, body []byte) {
	ptr, length := bytesPtr(body)
	rawActionRespond(int32(status), ptr, length)
}

func actionRewriteHost(host string) {
	ptr, length := stringPtr(host)
	rawActionRewriteHost(ptr, length)
}

func actionRewritePath(path string) {
	ptr, length := stringPtr(path)
	rawActionRewritePath(ptr, length)
}

func actionRewriteQuery(query string) {
	ptr, length := stringPtr(query)
	rawActionRewriteQuery(ptr, length)
}

func actionReqHeaderSet(name, value string) {
	namePtr, nameLen := stringPtr(name)
	valuePtr, valueLen := stringPtr(value)
	rawActionReqHeaderSet(namePtr, nameLen, valuePtr, valueLen)
}

func actionReqHeaderAdd(name, value string) {
	namePtr, nameLen := stringPtr(name)
	valuePtr, valueLen := stringPtr(value)
	rawActionReqHeaderAdd(namePtr, nameLen, valuePtr, valueLen)
}

func actionReqHeaderDelete(name string) {
	namePtr, nameLen := stringPtr(name)
	rawActionReqHeaderDelete(namePtr, nameLen)
}

func actionRespHeaderSet(name, value string) {
	namePtr, nameLen := stringPtr(name)
	valuePtr, valueLen := stringPtr(value)
	rawActionRespHeaderSet(namePtr, nameLen, valuePtr, valueLen)
}

func actionRespHeaderAdd(name, value string) {
	namePtr, nameLen := stringPtr(name)
	valuePtr, valueLen := stringPtr(value)
	rawActionRespHeaderAdd(namePtr, nameLen, valuePtr, valueLen)
}

func actionRespHeaderDelete(name string) {
	namePtr, nameLen := stringPtr(name)
	rawActionRespHeaderDelete(namePtr, nameLen)
}

func actionSetMetadata(key, value string) {
	keyPtr, keyLen := stringPtr(key)
	valuePtr, valueLen := stringPtr(value)
	rawActionSetMetadata(keyPtr, keyLen, valuePtr, valueLen)
}

func actionSetReason(reason string) {
	ptr, length := stringPtr(reason)
	rawActionSetReason(ptr, length)
}

func stringPtr(value string) (uint32, uint32) {
	if len(value) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.StringData(value)))), uint32(len(value))
}

func bytesPtr(value []byte) (uint32, uint32) {
	if len(value) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(&value[0]))), uint32(len(value))
}
