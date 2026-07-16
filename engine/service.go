package engine

import (
	"context"
	"errors"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/internal/bundlecache"
	"github.com/ethndotsh/switchboard/registry"
	"go.uber.org/zap"
)

var ErrNoActiveRuntime = errors.New("no active switchboard runtime")

type Service struct {
	manager    *RuntimeManager
	runtime    WasmRuntime
	reconciler *Reconciler
	cancel     context.CancelFunc
	failMode   string
	logger     *zap.Logger
}

type InvokeResult struct {
	Action       switchboard.Action
	BundleID     string
	UsedLastGood bool
}

type PoolStats struct {
	MinSize        int   `json:"min_size"`
	MaxSize        int   `json:"max_size"`
	TargetSize     int   `json:"target_size"`
	TotalInstances int64 `json:"total_instances"`
	Inflight       int64 `json:"inflight"`
	Exhaustions    int64 `json:"exhaustions_total"`
}

type ServiceStatus struct {
	ActiveBundleID   string          `json:"active_bundle_id,omitempty"`
	LastGoodBundleID string          `json:"last_good_bundle_id,omitempty"`
	FailMode         string          `json:"fail_mode"`
	Reconciler       ReconcilerState `json:"reconciler"`
	Pool             PoolStats       `json:"pool"`
}

func Start(ctx context.Context, cfg Config, logger *zap.Logger) (*Service, error) {
	resolved, err := ResolveConfig(cfg)
	if err != nil {
		return nil, err
	}
	baseCtx, cancel := context.WithCancel(ctx)

	runtimeOpts := RuntimeOptions{
		CloseOnContextDone: true,
		MemoryLimitPages:   bytesToWasmPages(resolved.MemoryLimitBytes),
	}
	if resolved.CacheDir != "" {
		runtimeOpts.CacheDir = bundlecache.WazeroCacheDir(resolved.CacheDir)
	}
	wasmRuntime, err := NewWazeroRuntime(baseCtx, runtimeOpts)
	if err != nil {
		cancel()
		return nil, err
	}

	manager := &RuntimeManager{logger: logger}

	var cache *bundlecache.Cache
	if resolved.CacheDir != "" {
		cache = bundlecache.New(resolved.CacheDir)
		if resolved.BootstrapFromCache {
			bootstrapFromCache(baseCtx, cache, manager, wasmRuntime, resolved, logger)
		}
	}

	// Registry construction is config-only so an unreachable store does not
	// block proxy startup; the reconciler retries in the background.
	reg, err := registry.Open(baseCtx, resolved.RegistryURL)
	if err != nil {
		cancel()
		_ = manager.Close(baseCtx)
		_ = wasmRuntime.Close(baseCtx)
		return nil, err
	}

	reconciler := NewReconciler(reg, manager, wasmRuntime, resolved, logger)
	reconciler.cache = cache
	go reconciler.Run(baseCtx)
	return &Service{
		manager:    manager,
		runtime:    wasmRuntime,
		reconciler: reconciler,
		cancel:     cancel,
		failMode:   resolved.FailMode,
		logger:     logger,
	}, nil
}

// bootstrapFromCache best-effort activates the locally cached bundle;
// failures log and fall through to remote reconciliation.
func bootstrapFromCache(ctx context.Context, cache *bundlecache.Cache, manager *RuntimeManager, wasmRuntime WasmRuntime, cfg ResolvedConfig, logger *zap.Logger) {
	log := logger
	if log == nil {
		log = zap.NewNop()
	}
	b, meta, err := cache.Load(cfg.Namespace, cfg.Channel)
	if err != nil {
		log.Debug("no usable switchboard bundle cache", zap.String("namespace", cfg.Namespace), zap.String("channel", cfg.Channel), zap.Error(err))
		return
	}
	poolCfg := PoolConfig{MinSize: cfg.MinPoolSize, MaxSize: cfg.MaxPoolSize, Autoscale: cfg.PoolAutoscale}
	candidate, err := NewRuntimeWithPoolConfig(ctx, wasmRuntime, b, cfg.invokeLimits(), poolCfg, logger)
	if err != nil {
		log.Warn("failed to compile cached switchboard bundle", zap.String("bundle_id", b.ID), zap.Error(err))
		return
	}
	if err := candidate.Validate(ctx); err != nil {
		_ = candidate.Close(ctx)
		log.Warn("cached switchboard bundle failed validation", zap.String("bundle_id", b.ID), zap.Error(err))
		return
	}
	manager.Activate(candidate)
	log.Info("bootstrapped switchboard bundle from cache",
		zap.String("bundle_id", b.ID),
		zap.String("namespace", cfg.Namespace),
		zap.String("channel", cfg.Channel),
		zap.Time("originally_activated_at", meta.ActivatedAt),
	)
}

func (s *Service) Current() *Runtime {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Current()
}

// InvokeWithFallback runs the request against the active runtime; with
// fail_mode last_good a failure retries once against the previous runtime.
func (s *Service) InvokeWithFallback(ctx context.Context, req switchboard.Request) (InvokeResult, error) {
	if s == nil || s.manager == nil {
		return InvokeResult{}, ErrNoActiveRuntime
	}
	active := s.manager.Current()
	if active == nil {
		if s.failMode == FailModeLastGood {
			if result, ok := s.invokeLastGood(ctx, req, nil); ok {
				return result, nil
			}
		}
		return InvokeResult{}, ErrNoActiveRuntime
	}
	action, err := active.Invoke(ctx, req)
	if err == nil {
		return InvokeResult{Action: action, BundleID: active.ID()}, nil
	}
	s.log().Warn("switchboard active runtime invocation failed", zap.String("bundle_id", active.ID()), zap.Error(err))
	if s.failMode == FailModeLastGood && ctx.Err() == nil {
		if result, ok := s.invokeLastGood(ctx, req, active); ok {
			return result, nil
		}
	}
	return InvokeResult{BundleID: active.ID()}, err
}

func (s *Service) invokeLastGood(ctx context.Context, req switchboard.Request, active *Runtime) (InvokeResult, bool) {
	lastGood := s.manager.LastGood()
	if lastGood == nil || lastGood == active || lastGood.IsClosed() {
		return InvokeResult{}, false
	}
	action, err := lastGood.Invoke(ctx, req)
	if err != nil {
		s.log().Warn("switchboard last-good runtime invocation failed", zap.String("bundle_id", lastGood.ID()), zap.Error(err))
		return InvokeResult{}, false
	}
	return InvokeResult{Action: action, BundleID: lastGood.ID(), UsedLastGood: true}, true
}

func (s *Service) State() ReconcilerState {
	if s == nil || s.reconciler == nil {
		return ReconcilerState{}
	}
	return s.reconciler.State()
}

func (s *Service) Status() ServiceStatus {
	status := ServiceStatus{FailMode: s.failMode, Reconciler: s.State()}
	if active := s.Current(); active != nil {
		status.ActiveBundleID = active.ID()
		status.Pool = active.Stats()
	}
	if s.manager != nil {
		if lastGood := s.manager.LastGood(); lastGood != nil {
			status.LastGoodBundleID = lastGood.ID()
		}
	}
	return status
}

func (s *Service) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.manager != nil {
		_ = s.manager.Close(ctx)
	}
	if s.runtime != nil {
		return s.runtime.Close(ctx)
	}
	return nil
}

func (s *Service) log() *zap.Logger {
	if s.logger == nil {
		return zap.NewNop()
	}
	return s.logger
}
