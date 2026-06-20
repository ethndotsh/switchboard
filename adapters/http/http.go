package httpadapter

import (
	"fmt"
	"net/http"

	"github.com/ethndotsh/switchboard"
)

func RequestFromHTTP(r *http.Request) switchboard.Request {
	return switchboard.Request{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header,
	}
}

func ApplyAction(w http.ResponseWriter, r *http.Request, action switchboard.Action) (bool, error) {
	switch action.Type {
	case "", switchboard.ActionNext:
		ApplyHeaderOps(r, action.HeaderOps)
		return true, nil
	case switchboard.ActionDeny:
		ApplyResponseHeaderOps(w.Header(), action.HeaderOps)
		status := action.StatusCode
		if status == 0 {
			status = http.StatusForbidden
		}
		w.WriteHeader(status)
		return false, nil
	case switchboard.ActionRedirect:
		ApplyResponseHeaderOps(w.Header(), action.HeaderOps)
		status := action.StatusCode
		if status == 0 {
			status = http.StatusFound
		}
		http.Redirect(w, r, action.Location, status)
		return false, nil
	case switchboard.ActionRewrite:
		ApplyHeaderOps(r, action.HeaderOps)
		if action.RewritePath != "" {
			r.URL.Path = action.RewritePath
			r.RequestURI = r.URL.RequestURI()
		}
		return true, nil
	default:
		return false, fmt.Errorf("unknown switchboard action %q", action.Type)
	}
}

func ApplyHeaderOps(r *http.Request, ops []switchboard.HeaderOp) {
	applyHeaderOps(r.Header, ops)
}

func ApplyResponseHeaderOps(h http.Header, ops []switchboard.HeaderOp) {
	applyHeaderOps(h, ops)
}

func applyHeaderOps(h http.Header, ops []switchboard.HeaderOp) {
	for _, op := range ops {
		switch op.Op {
		case switchboard.HeaderOpSet:
			h.Set(op.Name, op.Value)
		case switchboard.HeaderOpAdd:
			h.Add(op.Name, op.Value)
		case switchboard.HeaderOpDelete:
			h.Del(op.Name)
		}
	}
}
