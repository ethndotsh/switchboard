package sdk

import (
	"sort"

	"github.com/ethndotsh/switchboard"
)

type Decision = switchboard.Decision
type HeaderOpType = switchboard.HeaderOpType

// RequestData is the populated form of a request, used by host-side tests.
type RequestData = switchboard.Request

const (
	DecisionNext     = switchboard.DecisionNext
	DecisionDeny     = switchboard.DecisionDeny
	DecisionRedirect = switchboard.DecisionRedirect
	DecisionRewrite  = switchboard.DecisionRewrite
	DecisionRespond  = switchboard.DecisionRespond

	HeaderOpSet    = switchboard.HeaderOpSet
	HeaderOpAdd    = switchboard.HeaderOpAdd
	HeaderOpDelete = switchboard.HeaderOpDelete
)

// Request reads fields from populated RequestData (host tests) or lazily
// through host calls (inside the proxy); the patch overlay lets Chain show
// later rules the request as rewritten so far.
type Request struct {
	populated bool
	data      switchboard.Request
	patch     switchboard.RequestPatch
}

func CurrentRequest() Request {
	return Request{}
}

func NewRequest(data RequestData) Request {
	return Request{populated: true, data: data}
}

func (r Request) Method() string {
	if r.populated {
		return r.data.Method
	}
	return requestMethod()
}

func (r Request) Path() string {
	if r.patch.Path != nil {
		return *r.patch.Path
	}
	if r.populated {
		return r.data.Path
	}
	return requestPath()
}

func (r Request) Host() string {
	if r.patch.Host != nil {
		return *r.patch.Host
	}
	if r.populated {
		return r.data.Host
	}
	return requestHost()
}

func (r Request) RawQuery() string {
	if r.patch.Query != nil {
		return *r.patch.Query
	}
	if r.populated {
		return r.data.RawQuery
	}
	return requestRawQuery()
}

func (r Request) Query(name string) string {
	if r.patch.Query != nil || r.populated {
		return queryValue(r.RawQuery(), name)
	}
	return requestQueryValue(name)
}

func (r Request) Scheme() string {
	if r.populated {
		return r.data.Scheme
	}
	return requestScheme()
}

func (r Request) Protocol() string {
	if r.populated {
		return r.data.Protocol
	}
	return requestProtocol()
}

func (r Request) RemoteAddr() string {
	if r.populated {
		return r.data.RemoteAddr
	}
	return requestRemoteAddr()
}

// ClientIP is adapter-resolved, honoring the proxy's trusted-proxy config.
func (r Request) ClientIP() string {
	if r.populated {
		return r.data.ClientIP
	}
	return requestClientIP()
}

func (r Request) TLS() bool {
	if r.populated {
		return r.data.TLS
	}
	return requestTLS()
}

func (r Request) Header(name string) string {
	values := r.HeaderValues(name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (r Request) HeaderValues(name string) []string {
	var base []string
	if r.populated {
		base = headerLookup(r.data.Headers, name)
	} else {
		base = requestHeaderValues(name)
	}
	return applyHeaderOpsFor(name, base, r.patch.Headers)
}

func (r Request) Cookie(name string) string {
	if r.populated || len(r.patch.Headers) > 0 {
		for _, header := range r.HeaderValues("Cookie") {
			if value, ok := cookieValue(header, name); ok {
				return value
			}
		}
		return ""
	}
	return requestCookie(name)
}

func (r Request) WithPatch(p switchboard.RequestPatch) Request {
	if p.IsZero() {
		return r
	}
	if len(p.Headers) > 0 {
		r.patch.Headers = append(append([]switchboard.HeaderOp(nil), r.patch.Headers...), p.Headers...)
	}
	if p.Host != nil {
		r.patch.Host = p.Host
	}
	if p.Path != nil {
		r.patch.Path = p.Path
	}
	if p.Query != nil {
		r.patch.Query = p.Query
	}
	return r
}

type Action struct {
	Decision switchboard.Decision
	Patch    switchboard.RequestPatch
	Response switchboard.Response
	Metadata map[string]string
	Reason   string
}

func Next() Action {
	return Action{Decision: DecisionNext}
}

func Deny(status int) Action {
	if status == 0 {
		status = 403
	}
	return Action{Decision: DecisionDeny, Response: switchboard.Response{Status: status}}
}

func Redirect(status int, location string) Action {
	if status == 0 {
		status = 302
	}
	return Action{Decision: DecisionRedirect, Response: switchboard.Response{Status: status, Location: location}}
}

func Rewrite(path string) Action {
	return Action{Decision: DecisionRewrite, Patch: switchboard.RequestPatch{Path: &path}}
}

func Respond(status int, body string) Action {
	if status == 0 {
		status = 200
	}
	return Action{Decision: DecisionRespond, Response: switchboard.Response{Status: status, Body: []byte(body)}}
}

func (a Action) SetRequestHeader(name, value string) Action {
	a.Patch.Headers = append(a.Patch.Headers, switchboard.HeaderOp{Op: HeaderOpSet, Name: name, Value: value})
	return a
}

func (a Action) AddRequestHeader(name, value string) Action {
	a.Patch.Headers = append(a.Patch.Headers, switchboard.HeaderOp{Op: HeaderOpAdd, Name: name, Value: value})
	return a
}

func (a Action) DeleteRequestHeader(name string) Action {
	a.Patch.Headers = append(a.Patch.Headers, switchboard.HeaderOp{Op: HeaderOpDelete, Name: name})
	return a
}

func (a Action) SetResponseHeader(name, value string) Action {
	a.Response.Headers = append(a.Response.Headers, switchboard.HeaderOp{Op: HeaderOpSet, Name: name, Value: value})
	return a
}

func (a Action) AddResponseHeader(name, value string) Action {
	a.Response.Headers = append(a.Response.Headers, switchboard.HeaderOp{Op: HeaderOpAdd, Name: name, Value: value})
	return a
}

func (a Action) DeleteResponseHeader(name string) Action {
	a.Response.Headers = append(a.Response.Headers, switchboard.HeaderOp{Op: HeaderOpDelete, Name: name})
	return a
}

func (a Action) RewriteHost(host string) Action {
	a.Patch.Host = &host
	return a
}

func (a Action) RewritePath(path string) Action {
	a.Patch.Path = &path
	return a
}

func (a Action) RewriteQuery(query string) Action {
	a.Patch.Query = &query
	return a
}

func (a Action) WithReason(reason string) Action {
	a.Reason = reason
	return a
}

// SetMetadata values are exposed by the proxy adapter to the rest of the
// handler chain (Caddy request variables).
func (a Action) SetMetadata(name, value string) Action {
	if a.Metadata == nil {
		a.Metadata = map[string]string{}
	}
	a.Metadata[name] = value
	return a
}

func (a Action) emit() int32 {
	switch a.Decision {
	case "", DecisionNext:
		actionNext()
	case DecisionDeny:
		actionDeny(int32(a.Response.Status))
	case DecisionRedirect:
		actionRedirect(a.Response.Status, a.Response.Location)
	case DecisionRewrite:
		actionRewrite()
	case DecisionRespond:
		actionRespond(a.Response.Status, a.Response.Body)
	default:
		return 1
	}
	if a.Patch.Host != nil {
		actionRewriteHost(*a.Patch.Host)
	}
	if a.Patch.Path != nil {
		actionRewritePath(*a.Patch.Path)
	}
	if a.Patch.Query != nil {
		actionRewriteQuery(*a.Patch.Query)
	}
	for _, op := range a.Patch.Headers {
		switch op.Op {
		case HeaderOpSet:
			actionReqHeaderSet(op.Name, op.Value)
		case HeaderOpAdd:
			actionReqHeaderAdd(op.Name, op.Value)
		case HeaderOpDelete:
			actionReqHeaderDelete(op.Name)
		default:
			return 1
		}
	}
	for _, op := range a.Response.Headers {
		switch op.Op {
		case HeaderOpSet:
			actionRespHeaderSet(op.Name, op.Value)
		case HeaderOpAdd:
			actionRespHeaderAdd(op.Name, op.Value)
		case HeaderOpDelete:
			actionRespHeaderDelete(op.Name)
		default:
			return 1
		}
	}
	if len(a.Metadata) > 0 {
		keys := make([]string, 0, len(a.Metadata))
		for key := range a.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			actionSetMetadata(key, a.Metadata[key])
		}
	}
	if a.Reason != "" {
		actionSetReason(a.Reason)
	}
	return 0
}

func Return(action Action) int32 {
	return action.emit()
}
