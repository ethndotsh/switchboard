package sdk

import "github.com/ethndotsh/switchboard"

type ActionType = switchboard.ActionType
type HeaderOpType = switchboard.HeaderOpType

const (
	ActionNext     = switchboard.ActionNext
	ActionDeny     = switchboard.ActionDeny
	ActionRedirect = switchboard.ActionRedirect
	ActionRewrite  = switchboard.ActionRewrite

	HeaderOpSet    = switchboard.HeaderOpSet
	HeaderOpAdd    = switchboard.HeaderOpAdd
	HeaderOpDelete = switchboard.HeaderOpDelete
)

type Request struct {
	method  string
	path    string
	headers map[string][]string
}

type Action struct {
	Type        ActionType
	StatusCode  int
	Location    string
	RewritePath string
	HeaderOps   []switchboard.HeaderOp
}

func NewRequest(method, path string, headers map[string][]string) Request {
	return Request{method: method, path: path, headers: headers}
}

func CurrentRequest() Request {
	return Request{}
}

func (r Request) Method() string {
	if r.method != "" || r.path != "" || r.headers != nil {
		return r.method
	}
	return requestMethod()
}

func (r Request) Path() string {
	if r.method != "" || r.path != "" || r.headers != nil {
		return r.path
	}
	return requestPath()
}

func (r Request) Header(name string) string {
	values := r.HeaderValues(name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (r Request) HeaderValues(name string) []string {
	if r.method != "" || r.path != "" || r.headers != nil {
		return append([]string(nil), r.headers[name]...)
	}
	return requestHeaderValues(name)
}

func (r Request) WithHeaderOps(ops []switchboard.HeaderOp) Request {
	if len(ops) == 0 {
		return r
	}
	headers := make(map[string][]string, len(r.headers)+len(ops))
	for key, values := range r.headers {
		headers[key] = append([]string(nil), values...)
	}
	for _, op := range ops {
		switch op.Op {
		case HeaderOpSet:
			headers[op.Name] = []string{op.Value}
		case HeaderOpAdd:
			headers[op.Name] = append(headers[op.Name], op.Value)
		case HeaderOpDelete:
			delete(headers, op.Name)
		}
	}
	r.headers = headers
	return r
}

func Next() Action {
	return Action{Type: ActionNext}
}

func Deny(status int) Action {
	if status == 0 {
		status = 403
	}
	return Action{Type: ActionDeny, StatusCode: status}
}

func Redirect(status int, location string) Action {
	if status == 0 {
		status = 302
	}
	return Action{Type: ActionRedirect, StatusCode: status, Location: location}
}

func Rewrite(path string) Action {
	return Action{Type: ActionRewrite, RewritePath: path}
}

func (a Action) SetHeader(name, value string) Action {
	a.HeaderOps = append(a.HeaderOps, switchboard.HeaderOp{Op: HeaderOpSet, Name: name, Value: value})
	return a
}

func (a Action) AddHeader(name, value string) Action {
	a.HeaderOps = append(a.HeaderOps, switchboard.HeaderOp{Op: HeaderOpAdd, Name: name, Value: value})
	return a
}

func (a Action) DeleteHeader(name string) Action {
	a.HeaderOps = append(a.HeaderOps, switchboard.HeaderOp{Op: HeaderOpDelete, Name: name})
	return a
}

func (a Action) emit() int32 {
	switch a.Type {
	case "", ActionNext:
		actionNext()
	case ActionDeny:
		actionDeny(int32(a.StatusCode))
	case ActionRedirect:
		actionRedirect(a.StatusCode, a.Location)
	case ActionRewrite:
		actionRewrite(a.RewritePath)
	default:
		return 1
	}
	for _, op := range a.HeaderOps {
		switch op.Op {
		case HeaderOpSet:
			actionHeaderSet(op.Name, op.Value)
		case HeaderOpAdd:
			actionHeaderAdd(op.Name, op.Value)
		case HeaderOpDelete:
			actionHeaderDelete(op.Name)
		default:
			return 1
		}
	}
	return 0
}

func Return(action Action) int32 {
	return action.emit()
}
