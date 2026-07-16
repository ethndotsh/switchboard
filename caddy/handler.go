package caddy

import (
	"context"
	"fmt"
	"net/http"

	caddyserver "github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	httpadapter "github.com/ethndotsh/switchboard/adapters/http"
	"github.com/ethndotsh/switchboard/engine"
	"go.uber.org/zap"
)

func init() {
	caddyserver.RegisterModule(Switchboard{})
	httpcaddyfile.RegisterHandlerDirective("switchboard", parseCaddyfile)
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var s Switchboard
	err := s.UnmarshalCaddyfile(h.Dispenser)
	return &s, err
}

type Switchboard struct {
	Registry           string `json:"registry,omitempty"`
	RegistryURL        string `json:"registry_url,omitempty"`
	Namespace          string `json:"namespace,omitempty"`
	Channel            string `json:"channel,omitempty"`
	PollInterval       string `json:"poll_interval,omitempty"`
	FailMode           string `json:"fail_mode,omitempty"`
	FallbackFailMode   string `json:"fallback_fail_mode,omitempty"`
	InvokeTimeout      string `json:"invoke_timeout,omitempty"`
	MemoryLimit        string `json:"memory_limit,omitempty"`
	MaxActionBytes     string `json:"max_action_bytes,omitempty"`
	MaxHeaderOps       int    `json:"max_header_ops,omitempty"`
	MaxResponseBody    string `json:"max_response_body,omitempty"`
	CacheDir           string `json:"cache_dir,omitempty"`
	BootstrapFromCache string `json:"bootstrap_from_cache,omitempty"`
	PoolAutoscale      string `json:"pool_autoscale,omitempty"`
	PoolSize           int    `json:"pool_size,omitempty"`
	MinPoolSize        int    `json:"min_pool_size,omitempty"`
	MaxPoolSize        int    `json:"max_pool_size,omitempty"`

	service *engine.Service
	manager *engine.RuntimeManager
	logger  *zap.Logger
}

func (Switchboard) CaddyModule() caddyserver.ModuleInfo {
	return caddyserver.ModuleInfo{
		ID:  "http.handlers.switchboard",
		New: func() caddyserver.Module { return new(Switchboard) },
	}
}

func (s *Switchboard) Provision(ctx caddyserver.Context) error {
	s.logger = ctx.Logger(s)
	service, err := engine.Start(context.Background(), engine.Config{
		Registry:           s.Registry,
		RegistryURL:        s.RegistryURL,
		Namespace:          s.Namespace,
		Channel:            s.Channel,
		PollInterval:       s.PollInterval,
		FailMode:           s.FailMode,
		FallbackFailMode:   s.FallbackFailMode,
		InvokeTimeout:      s.InvokeTimeout,
		MemoryLimit:        s.MemoryLimit,
		MaxActionBytes:     s.MaxActionBytes,
		MaxHeaderOps:       s.MaxHeaderOps,
		MaxResponseBody:    s.MaxResponseBody,
		CacheDir:           s.CacheDir,
		BootstrapFromCache: s.BootstrapFromCache,
		PoolAutoscale:      s.PoolAutoscale,
		PoolSize:           s.PoolSize,
		MinPoolSize:        s.MinPoolSize,
		MaxPoolSize:        s.MaxPoolSize,
	}, s.logger)
	if err != nil {
		return err
	}
	s.service = service
	return nil
}

func (s *Switchboard) Cleanup() error {
	if s.service != nil {
		return s.service.Close(context.Background())
	}
	return nil
}

func (s *Switchboard) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	req := httpadapter.RequestFromHTTP(r)
	// Prefer Caddy's client IP, which honors trusted_proxies.
	if clientIP, ok := caddyhttp.GetVar(r.Context(), caddyhttp.ClientIPVarKey).(string); ok && clientIP != "" {
		req.ClientIP = clientIP
	}

	var result engine.InvokeResult
	var err error
	if s.service != nil {
		result, err = s.service.InvokeWithFallback(r.Context(), req)
	} else {
		current := s.currentRuntime()
		if current == nil {
			err = engine.ErrNoActiveRuntime
		} else {
			result.BundleID = current.ID()
			result.Action, err = current.Invoke(r.Context(), req)
		}
	}
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("switchboard rule invocation failed", zap.String("bundle_id", result.BundleID), zap.Error(err))
		}
		return s.handleUnavailable(w, r, next, err.Error(), result.BundleID)
	}

	s.exposeDecision(r, result)

	callNext, err := httpadapter.ApplyAction(w, r, result.Action)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("unknown switchboard action", zap.String("decision", string(result.Action.Decision)), zap.Error(err))
		}
		return s.handleUnavailable(w, r, next, err.Error(), result.BundleID)
	}
	if callNext {
		return next.ServeHTTP(w, r)
	}
	return nil
}

// exposeDecision maps rule metadata to Caddy request variables and appends
// decision fields to access logs.
func (s *Switchboard) exposeDecision(r *http.Request, result engine.InvokeResult) {
	ctx := r.Context()
	for key, value := range result.Action.Metadata {
		caddyhttp.SetVar(ctx, key, value)
	}
	caddyhttp.SetVar(ctx, "switchboard.decision", string(result.Action.Decision))
	if result.Action.Reason != "" {
		caddyhttp.SetVar(ctx, "switchboard.reason", result.Action.Reason)
	}
	if extra, ok := ctx.Value(caddyhttp.ExtraLogFieldsCtxKey).(*caddyhttp.ExtraLogFields); ok && extra != nil {
		extra.Set(zap.String("switchboard_decision", string(result.Action.Decision)))
		extra.Set(zap.String("switchboard_bundle_id", result.BundleID))
		if result.Action.Reason != "" {
			extra.Set(zap.String("switchboard_reason", result.Action.Reason))
		}
	}
	// Check first so the field slice is not allocated per request when
	// debug logging is off.
	if s.logger != nil && s.logger.Core().Enabled(zap.DebugLevel) {
		s.logger.Debug("switchboard decision",
			zap.String("decision", string(result.Action.Decision)),
			zap.String("reason", result.Action.Reason),
			zap.String("bundle_id", result.BundleID),
			zap.Bool("used_last_good", result.UsedLastGood),
		)
	}
}

func (s *Switchboard) handleUnavailable(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler, reason string, bundleID string) error {
	if s.effectiveFailMode() == engine.FailModeClosed {
		if s.logger != nil {
			s.logger.Warn("switchboard fail-closed decision", zap.String("reason", reason), zap.String("bundle_id", bundleID))
		}
		http.Error(w, "switchboard rule unavailable", http.StatusServiceUnavailable)
		return nil
	}
	if s.logger != nil {
		s.logger.Warn("switchboard fail-open decision", zap.String("reason", reason), zap.String("bundle_id", bundleID))
	}
	return next.ServeHTTP(w, r)
}

func (s *Switchboard) effectiveFailMode() string {
	switch s.FailMode {
	case engine.FailModeLastGood:
		if s.FallbackFailMode == engine.FailModeClosed {
			return engine.FailModeClosed
		}
		return engine.FailModeOpen
	case engine.FailModeClosed:
		return engine.FailModeClosed
	default:
		return engine.FailModeOpen
	}
}

func (s *Switchboard) currentRuntime() *engine.Runtime {
	if s.service != nil {
		return s.service.Current()
	}
	if s.manager != nil {
		return s.manager.Current()
	}
	return nil
}

func (s *Switchboard) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	stringArg := func(target *string) error {
		if !d.NextArg() {
			return d.ArgErr()
		}
		*target = d.Val()
		return nil
	}
	positiveIntArg := func(target *int, name string) error {
		if !d.NextArg() {
			return d.ArgErr()
		}
		var value int
		if _, err := fmt.Sscanf(d.Val(), "%d", &value); err != nil {
			return d.Errf("invalid %s %q", name, d.Val())
		}
		if value <= 0 {
			return d.Errf("invalid %s %q", name, d.Val())
		}
		*target = value
		return nil
	}
	for d.Next() {
		for d.NextBlock(0) {
			var err error
			switch d.Val() {
			case "registry":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.Registry = d.Val()
				if d.NextArg() {
					s.RegistryURL = d.Val()
				}
			case "channel":
				err = stringArg(&s.Channel)
			case "namespace":
				err = stringArg(&s.Namespace)
			case "poll_interval":
				err = stringArg(&s.PollInterval)
			case "fail_mode":
				err = stringArg(&s.FailMode)
			case "fallback_fail_mode":
				err = stringArg(&s.FallbackFailMode)
			case "invoke_timeout":
				err = stringArg(&s.InvokeTimeout)
			case "memory_limit":
				err = stringArg(&s.MemoryLimit)
			case "max_action_bytes":
				err = stringArg(&s.MaxActionBytes)
			case "max_header_ops":
				err = positiveIntArg(&s.MaxHeaderOps, "max_header_ops")
			case "max_response_body":
				err = stringArg(&s.MaxResponseBody)
			case "cache_dir":
				err = stringArg(&s.CacheDir)
			case "bootstrap_from_cache":
				if !d.NextArg() {
					return d.ArgErr()
				}
				switch d.Val() {
				case "on", "off", "true", "false":
					s.BootstrapFromCache = d.Val()
				default:
					return d.Errf("invalid bootstrap_from_cache %q", d.Val())
				}
			case "pool_size":
				err = positiveIntArg(&s.PoolSize, "pool_size")
			case "pool_autoscale":
				if !d.NextArg() {
					return d.ArgErr()
				}
				switch d.Val() {
				case "on", "off", "true", "false":
					s.PoolAutoscale = d.Val()
				default:
					return d.Errf("invalid pool_autoscale %q", d.Val())
				}
			case "min_pool_size":
				err = positiveIntArg(&s.MinPoolSize, "min_pool_size")
			case "max_pool_size":
				err = positiveIntArg(&s.MaxPoolSize, "max_pool_size")
			default:
				return d.Errf("unrecognized switchboard directive %q", d.Val())
			}
			if err != nil {
				return err
			}
		}
	}
	switch s.FailMode {
	case "", engine.FailModeOpen, engine.FailModeClosed, engine.FailModeLastGood:
	default:
		return d.Errf("invalid fail_mode %q; expected open, closed, or last_good", s.FailMode)
	}
	switch s.FallbackFailMode {
	case "":
	case engine.FailModeOpen, engine.FailModeClosed:
		if s.FailMode != engine.FailModeLastGood {
			return d.Errf("fallback_fail_mode requires fail_mode last_good")
		}
	default:
		return d.Errf("invalid fallback_fail_mode %q; expected open or closed", s.FallbackFailMode)
	}
	if s.BootstrapFromCache != "" && s.CacheDir == "" {
		return d.Errf("bootstrap_from_cache requires cache_dir")
	}
	if s.MinPoolSize > 0 && s.MaxPoolSize > 0 && s.MaxPoolSize < s.MinPoolSize {
		return d.Errf("max_pool_size must be greater than or equal to min_pool_size")
	}
	return nil
}

var (
	_ caddyserver.Provisioner     = (*Switchboard)(nil)
	_ caddyserver.CleanerUpper    = (*Switchboard)(nil)
	_ caddyhttp.MiddlewareHandler = (*Switchboard)(nil)
	_ caddyfile.Unmarshaler       = (*Switchboard)(nil)
)
