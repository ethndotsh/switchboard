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
	Registry      string `json:"registry,omitempty"`
	RegistryURL   string `json:"registry_url,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	Channel       string `json:"channel,omitempty"`
	PollInterval  string `json:"poll_interval,omitempty"`
	FailMode      string `json:"fail_mode,omitempty"`
	InvokeTimeout string `json:"invoke_timeout,omitempty"`
	PoolSize      int    `json:"pool_size,omitempty"`

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
		Registry:      s.Registry,
		RegistryURL:   s.RegistryURL,
		Namespace:     s.Namespace,
		Channel:       s.Channel,
		PollInterval:  s.PollInterval,
		FailMode:      s.FailMode,
		InvokeTimeout: s.InvokeTimeout,
		PoolSize:      s.PoolSize,
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
	current := s.currentRuntime()
	if current == nil {
		return s.handleUnavailable(w, r, next, "no active runtime", "")
	}
	action, err := current.Invoke(r.Context(), httpadapter.RequestFromHTTP(r))
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("switchboard rule invocation failed", zap.String("bundle_id", current.ID()), zap.Error(err))
		}
		return s.handleUnavailable(w, r, next, err.Error(), current.ID())
	}
	callNext, err := httpadapter.ApplyAction(w, r, action)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("unknown switchboard action", zap.String("action", string(action.Type)), zap.Error(err))
		}
		return s.handleUnavailable(w, r, next, err.Error(), current.ID())
	}
	if callNext {
		return next.ServeHTTP(w, r)
	}
	return nil
}

func (s *Switchboard) handleUnavailable(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler, reason string, bundleID string) error {
	if s.FailMode == "closed" {
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
	for d.Next() {
		for d.NextBlock(0) {
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
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.Channel = d.Val()
			case "namespace":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.Namespace = d.Val()
			case "poll_interval":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.PollInterval = d.Val()
			case "fail_mode":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.FailMode = d.Val()
			case "invoke_timeout":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.InvokeTimeout = d.Val()
			case "pool_size":
				if !d.NextArg() {
					return d.ArgErr()
				}
				var poolSize int
				if _, err := fmt.Sscanf(d.Val(), "%d", &poolSize); err != nil {
					return d.Errf("invalid pool_size %q", d.Val())
				}
				s.PoolSize = poolSize
			default:
				return d.Errf("unrecognized switchboard directive %q", d.Val())
			}
		}
	}
	return nil
}

var (
	_ caddyserver.Provisioner     = (*Switchboard)(nil)
	_ caddyserver.CleanerUpper    = (*Switchboard)(nil)
	_ caddyhttp.MiddlewareHandler = (*Switchboard)(nil)
	_ caddyfile.Unmarshaler       = (*Switchboard)(nil)
)
