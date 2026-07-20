package httpadapter

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ethndotsh/switchboard/engine"
)

// Instrumentation records metrics and surfaces decision metadata for a rule
// invocation. Both the standalone Middleware and the Caddy handler route
// through it so observability is identical across adapters.
type Instrumentation struct {
	Metrics    *Metrics
	OnDecision func(r *http.Request, result engine.InvokeResult)
}

// Observe records the invocation's metrics and, on success, surfaces its
// decision. It is safe to call on a zero Instrumentation.
func (in Instrumentation) Observe(r *http.Request, result engine.InvokeResult, err error, elapsed time.Duration) {
	if in.Metrics != nil {
		in.Metrics.ObserveInvocation(result.Action.Decision, err, elapsed)
	}
	if err == nil && in.OnDecision != nil {
		in.OnDecision(r, result)
	}
}

// StatusHandler serves the service's status as JSON. Front-ends mount it at a
// path of their choosing so the status contract is defined once.
func StatusHandler(service *engine.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := json.MarshalIndent(service.Status(), "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})
}
