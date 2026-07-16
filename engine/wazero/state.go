package wazero

import (
	"context"
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/ethndotsh/switchboard"
	"github.com/tetratelabs/wazero/api"
)

func invocationFromContext(ctx context.Context) *invocationState {
	state, _ := ctx.Value(invocationStateKey{}).(*invocationState)
	return state
}

// fail records the first violation; later action host calls become no-ops so
// the earliest, most precise error wins.
func (s *invocationState) fail(format string, args ...any) {
	if s.violation == nil {
		s.violation = fmt.Errorf(format, args...)
	}
}

func (s *invocationState) chargeBytes(n int) bool {
	s.actionBytes += n
	if s.limits.MaxActionBytes > 0 && s.actionBytes > s.limits.MaxActionBytes {
		s.fail("action output exceeds max_action_bytes %d", s.limits.MaxActionBytes)
		return false
	}
	return true
}

func (s *invocationState) appendHeaderOp(target headerTarget, op switchboard.HeaderOp) {
	if !validHeaderName(op.Name) {
		s.fail("header name %q is not a valid token", op.Name)
		return
	}
	if !validHeaderValue(op.Value) {
		s.fail("header %q value contains control characters", op.Name)
		return
	}
	if s.limits.MaxHeaderOps > 0 && s.headerOps >= s.limits.MaxHeaderOps {
		s.fail("header operations exceed max_header_ops %d", s.limits.MaxHeaderOps)
		return
	}
	if !s.chargeBytes(len(op.Name) + len(op.Value)) {
		return
	}
	s.headerOps++
	if target == headerTargetResponse {
		s.action.Response.Headers = append(s.action.Response.Headers, op)
		return
	}
	s.action.Patch.Headers = append(s.action.Patch.Headers, op)
}

func (s *invocationState) queryValue(name string) string {
	if !s.queryOnce {
		s.queryOnce = true
		if values, err := url.ParseQuery(s.request.RawQuery); err == nil {
			s.queryValues = values
		}
	}
	if s.queryValues == nil {
		return ""
	}
	return s.queryValues.Get(name)
}

func (s *invocationState) cookieValue(name string) string {
	values := headerValues(s.request.Headers, "Cookie")
	if len(values) == 0 {
		return ""
	}
	request := http.Request{Header: http.Header{"Cookie": values}}
	cookie, err := request.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func readGuestString(mod api.Module, ptr uint32, length uint32, max uint32) (string, bool) {
	if length == 0 {
		return "", true
	}
	if max > 0 && length > max {
		return "", false
	}
	data, ok := mod.Memory().Read(ptr, length)
	if !ok {
		return "", false
	}
	return string(data), true
}

// validHeaderName reports whether name is an RFC 7230 token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\r', '\n', 0:
			return false
		}
	}
	return true
}

// validTextValue rejects all C0 control characters except HTAB.
func validTextValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 && c != '\t' || c == 0x7f {
			return false
		}
	}
	return true
}

func headerValues(headers map[string][]string, name string) []string {
	if len(headers) == 0 || name == "" {
		return nil
	}
	if values, ok := headers[name]; ok {
		return values
	}
	if values, ok := headers[textproto.CanonicalMIMEHeaderKey(name)]; ok {
		return values
	}
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			return values
		}
	}
	return nil
}
