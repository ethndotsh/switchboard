package httpadapter

import (
	"net/http"
	"time"

	"github.com/ethndotsh/switchboard/engine"
	"go.uber.org/zap"
)

type Options struct {
	FailMode string
	Logger   *zap.Logger
	Metrics  *Metrics
	// OnDecision observes every successful decision, e.g. to expose
	// metadata as request variables.
	OnDecision func(r *http.Request, result engine.InvokeResult)
}

// Middleware is the standalone-mode and embeddable equivalent of the Caddy
// handler.
func Middleware(service *engine.Service, opts Options) func(http.Handler) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	instrumentation := Instrumentation{Metrics: opts.Metrics, OnDecision: opts.OnDecision}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			result, err := service.InvokeWithFallback(r.Context(), RequestFromHTTP(r))
			instrumentation.Observe(r, result, err, time.Since(start))
			if err != nil {
				logger.Warn("switchboard rule invocation failed", zap.String("bundle_id", result.BundleID), zap.Error(err))
				failUnavailable(w, r, next, opts.FailMode)
				return
			}
			callNext, err := ApplyAction(w, r, result.Action)
			if err != nil {
				logger.Warn("unknown switchboard action", zap.String("decision", string(result.Action.Decision)), zap.Error(err))
				failUnavailable(w, r, next, opts.FailMode)
				return
			}
			if callNext {
				next.ServeHTTP(w, r)
			}
		})
	}
}

func failUnavailable(w http.ResponseWriter, r *http.Request, next http.Handler, failMode string) {
	if failMode == engine.FailModeClosed {
		http.Error(w, "switchboard rule unavailable", http.StatusServiceUnavailable)
		return
	}
	next.ServeHTTP(w, r)
}
