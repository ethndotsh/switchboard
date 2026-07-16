//go:build !tinygo && !wasip1 && !wasm

package sdk

func requestMethod() string     { return "" }
func requestPath() string       { return "" }
func requestHost() string       { return "" }
func requestRawQuery() string   { return "" }
func requestScheme() string     { return "" }
func requestProtocol() string   { return "" }
func requestRemoteAddr() string { return "" }
func requestClientIP() string   { return "" }
func requestTLS() bool          { return false }

func requestHeaderValues(string) []string { return nil }
func requestQueryValue(string) string     { return "" }
func requestCookie(string) string         { return "" }

func actionNext()                       {}
func actionDeny(int32)                  {}
func actionRedirect(int, string)        {}
func actionRewrite()                    {}
func actionRespond(int, []byte)         {}
func actionRewriteHost(string)          {}
func actionRewritePath(string)          {}
func actionRewriteQuery(string)         {}
func actionReqHeaderSet(string, string) {}
func actionReqHeaderAdd(string, string) {}
func actionReqHeaderDelete(string)      {}

func actionRespHeaderSet(string, string) {}
func actionRespHeaderAdd(string, string) {}
func actionRespHeaderDelete(string)      {}
func actionSetMetadata(string, string)   {}
func actionSetReason(string)             {}
