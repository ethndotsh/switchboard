//go:build !tinygo && !wasip1 && !wasm

package sdk

func requestMethod() string { return "" }

func requestPath() string { return "" }

func requestHeaderValues(string) []string { return nil }

func actionNext() {}

func actionDeny(int32) {}

func actionRedirect(int, string) {}

func actionRewrite(string) {}

func actionHeaderSet(string, string) {}

func actionHeaderAdd(string, string) {}

func actionHeaderDelete(string) {}
