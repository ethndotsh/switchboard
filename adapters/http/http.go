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
	ApplyRequestHeaders(r, action.Headers)
	switch action.Type {
	case "", switchboard.ActionNext:
		return true, nil
	case switchboard.ActionDeny:
		status := action.StatusCode
		if status == 0 {
			status = http.StatusForbidden
		}
		w.WriteHeader(status)
		return false, nil
	case switchboard.ActionRedirect:
		status := action.StatusCode
		if status == 0 {
			status = http.StatusFound
		}
		http.Redirect(w, r, action.Location, status)
		return false, nil
	case switchboard.ActionRewrite:
		if action.RewritePath != "" {
			r.URL.Path = action.RewritePath
			r.RequestURI = r.URL.RequestURI()
		}
		return true, nil
	default:
		return false, fmt.Errorf("unknown switchboard action %q", action.Type)
	}
}

func ApplyRequestHeaders(r *http.Request, headers map[string]string) {
	for key, value := range headers {
		r.Header.Set(key, value)
	}
}
