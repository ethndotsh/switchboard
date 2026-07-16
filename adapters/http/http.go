package httpadapter

import (
	"fmt"
	"net"
	"net/http"

	"github.com/ethndotsh/switchboard"
)

// RequestFromHTTP defaults ClientIP to the connection's remote host; proxy
// adapters that know the trusted-proxy chain (Caddy) overwrite it.
func RequestFromHTTP(r *http.Request) switchboard.Request {
	scheme := "http"
	tls := r.TLS != nil
	if tls {
		scheme = "https"
	}
	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		clientIP = host
	}
	return switchboard.Request{
		Method:     r.Method,
		Scheme:     scheme,
		Host:       r.Host,
		Path:       r.URL.Path,
		RawQuery:   r.URL.RawQuery,
		Protocol:   r.Proto,
		Headers:    r.Header,
		RemoteAddr: r.RemoteAddr,
		ClientIP:   clientIP,
		TLS:        tls,
	}
}

// ApplyAction realizes an action and returns true when the request should
// continue down the handler chain. Redirect ignores any response body
// because http.Redirect writes its own.
func ApplyAction(w http.ResponseWriter, r *http.Request, action switchboard.Action) (bool, error) {
	switch action.Decision {
	case "", switchboard.DecisionNext, switchboard.DecisionRewrite:
		ApplyRequestPatch(r, action.Patch)
		return true, nil
	case switchboard.DecisionDeny:
		writeResponse(w, action.Response, http.StatusForbidden)
		return false, nil
	case switchboard.DecisionRespond:
		writeResponse(w, action.Response, http.StatusOK)
		return false, nil
	case switchboard.DecisionRedirect:
		ApplyResponseHeaderOps(w.Header(), action.Response.Headers)
		status := action.Response.Status
		if status == 0 {
			status = http.StatusFound
		}
		http.Redirect(w, r, action.Response.Location, status)
		return false, nil
	default:
		return false, fmt.Errorf("unknown switchboard decision %q", action.Decision)
	}
}

func ApplyRequestPatch(r *http.Request, patch switchboard.RequestPatch) {
	applyHeaderOps(r.Header, patch.Headers)
	urlChanged := false
	if patch.Host != nil {
		r.Host = *patch.Host
		r.URL.Host = *patch.Host
	}
	if patch.Path != nil {
		r.URL.Path = *patch.Path
		urlChanged = true
	}
	if patch.Query != nil {
		r.URL.RawQuery = *patch.Query
		urlChanged = true
	}
	if urlChanged {
		r.RequestURI = r.URL.RequestURI()
	}
}

func ApplyResponseHeaderOps(h http.Header, ops []switchboard.HeaderOp) {
	applyHeaderOps(h, ops)
}

func writeResponse(w http.ResponseWriter, response switchboard.Response, defaultStatus int) {
	ApplyResponseHeaderOps(w.Header(), response.Headers)
	status := response.Status
	if status == 0 {
		status = defaultStatus
	}
	if len(response.Body) > 0 && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(status)
	if len(response.Body) > 0 {
		_, _ = w.Write(response.Body)
	}
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
