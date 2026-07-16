package sdk

import (
	"strings"

	"github.com/ethndotsh/switchboard"
)

// queryValue is hand-rolled instead of net/url to keep TinyGo binaries
// small; parity with url.ParseQuery is covered by host-side tests.
func queryValue(rawQuery, name string) string {
	for rawQuery != "" {
		var pair string
		pair, rawQuery, _ = strings.Cut(rawQuery, "&")
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		decodedKey, ok := queryUnescape(key)
		if !ok || decodedKey != name {
			continue
		}
		decoded, ok := queryUnescape(value)
		if !ok {
			return ""
		}
		return decoded
	}
	return ""
}

func queryUnescape(s string) (string, bool) {
	if !strings.ContainsAny(s, "%+") {
		return s, true
	}
	var builder strings.Builder
	builder.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+':
			builder.WriteByte(' ')
		case '%':
			if i+2 >= len(s) {
				return "", false
			}
			hi, ok1 := hexDigit(s[i+1])
			lo, ok2 := hexDigit(s[i+2])
			if !ok1 || !ok2 {
				return "", false
			}
			builder.WriteByte(hi<<4 | lo)
			i += 2
		default:
			builder.WriteByte(s[i])
		}
	}
	return builder.String(), true
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func cookieValue(header, name string) (string, bool) {
	for header != "" {
		var pair string
		pair, header, _ = strings.Cut(header, ";")
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, found := strings.Cut(pair, "=")
		if !found || key != name {
			continue
		}
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		return value, true
	}
	return "", false
}

func headerLookup(headers map[string][]string, name string) []string {
	if len(headers) == 0 || name == "" {
		return nil
	}
	if values, ok := headers[name]; ok {
		return append([]string(nil), values...)
	}
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			return append([]string(nil), values...)
		}
	}
	return nil
}

func applyHeaderOpsFor(name string, base []string, ops []switchboard.HeaderOp) []string {
	if len(ops) == 0 {
		return base
	}
	values := base
	for _, op := range ops {
		if !strings.EqualFold(op.Name, name) {
			continue
		}
		switch op.Op {
		case HeaderOpSet:
			values = []string{op.Value}
		case HeaderOpAdd:
			values = append(append([]string(nil), values...), op.Value)
		case HeaderOpDelete:
			values = nil
		}
	}
	return values
}
