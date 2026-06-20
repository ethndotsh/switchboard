package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethndotsh/switchboard/registry"
	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

type Config struct {
	Registry      string
	RegistryURL   string
	Namespace     string
	Channel       string
	PollInterval  string
	FailMode      string
	InvokeTimeout string
	PoolSize      int
}

type ResolvedConfig struct {
	Registry      string
	RegistryURL   string
	Namespace     string
	Channel       string
	FailMode      string
	PollInterval  time.Duration
	InvokeTimeout time.Duration
	PoolSize      int
}

type ReconcilerState struct {
	Namespace                string
	Channel                  string
	ActiveBundleID           string
	DesiredBundleID          string
	LastSuccessfulActivation string
	LastFailedActivation     string
	LastError                string
	LastCheckedAt            time.Time
	LastSuccessAt            time.Time
	LastFailureAt            time.Time
}

type Reconciler struct {
	registry  registry.Registry
	manager   *RuntimeManager
	runtime   wazero.Runtime
	namespace string
	channel   string
	interval  time.Duration
	timeout   time.Duration
	poolSize  int
	logger    *zap.Logger

	mu    sync.RWMutex
	state ReconcilerState
}

type Watcher = Reconciler

type Service struct {
	manager *RuntimeManager
	runtime wazero.Runtime
	cancel  context.CancelFunc
}

func ResolveConfig(cfg Config) (ResolvedConfig, error) {
	resolved := ResolvedConfig{
		Registry:      cfg.Registry,
		RegistryURL:   cfg.RegistryURL,
		Namespace:     cfg.Namespace,
		Channel:       cfg.Channel,
		FailMode:      cfg.FailMode,
		PollInterval:  2 * time.Second,
		InvokeTimeout: 50 * time.Millisecond,
		PoolSize:      cfg.PoolSize,
	}
	if resolved.Channel == "" {
		resolved.Channel = "prod"
	}
	if err := registry.ValidateNamespace(resolved.Namespace); err != nil {
		return ResolvedConfig{}, err
	}
	if resolved.FailMode == "" {
		resolved.FailMode = "open"
	}
	if resolved.PoolSize <= 0 {
		resolved.PoolSize = DefaultPoolSize
	}
	if cfg.PollInterval != "" {
		interval, err := time.ParseDuration(cfg.PollInterval)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("invalid poll_interval: %w", err)
		}
		resolved.PollInterval = interval
	}
	if cfg.InvokeTimeout != "" {
		timeout, err := time.ParseDuration(cfg.InvokeTimeout)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("invalid invoke_timeout: %w", err)
		}
		resolved.InvokeTimeout = timeout
	}
	if resolved.Registry != "" && resolved.Registry != "s3" {
		return ResolvedConfig{}, fmt.Errorf("unsupported registry %q; Switchboard currently requires s3-compatible object storage", resolved.Registry)
	}
	return resolved, nil
}

func Start(ctx context.Context, cfg Config, logger *zap.Logger) (*Service, error) {
	resolved, err := ResolveConfig(cfg)
	if err != nil {
		return nil, err
	}
	s3cfg := registry.S3ConfigFromEnv()
	if resolved.RegistryURL != "" {
		parsed, err := registry.ParseS3URL(resolved.RegistryURL)
		if err != nil {
			return nil, err
		}
		s3cfg.Bucket = parsed.Bucket
		s3cfg.Prefix = parsed.Prefix
	}

	baseCtx, cancel := context.WithCancel(ctx)
	reg, err := registry.NewS3(baseCtx, s3cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	wasmRuntime, err := NewWasmRuntime(baseCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	manager := &RuntimeManager{}
	reconciler := NewReconciler(reg, manager, wasmRuntime, resolved, logger)
	go reconciler.Run(baseCtx)
	return &Service{manager: manager, runtime: wasmRuntime, cancel: cancel}, nil
}

func (s *Service) Current() *Runtime {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Current()
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

func NewReconciler(reg registry.Registry, manager *RuntimeManager, wasmRuntime wazero.Runtime, cfg ResolvedConfig, logger *zap.Logger) *Reconciler {
	return &Reconciler{
		registry:  reg,
		manager:   manager,
		runtime:   wasmRuntime,
		namespace: cfg.Namespace,
		channel:   cfg.Channel,
		interval:  cfg.PollInterval,
		timeout:   cfg.InvokeTimeout,
		poolSize:  cfg.PoolSize,
		logger:    logger,
	}
}

func (r *Reconciler) Run(ctx context.Context) {
	if r.interval <= 0 {
		r.interval = 2 * time.Second
	}
	r.reconcile(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *Reconciler) State() ReconcilerState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *Reconciler) reconcile(ctx context.Context) {
	now := time.Now().UTC()
	activeID := ""
	if current := r.managerCurrent(); current != nil {
		activeID = current.ID()
	}
	r.updateState(func(state *ReconcilerState) {
		state.Namespace = r.namespace
		state.Channel = r.channel
		state.ActiveBundleID = activeID
		state.LastCheckedAt = now
	})
	r.log().Info("switchboard reconcile check", zap.String("namespace", r.namespace), zap.String("channel", r.channel), zap.String("active_bundle_id", activeID))

	if r.registry == nil || r.manager == nil || r.runtime == nil {
		err := errors.New("switchboard reconciler is not fully configured")
		r.recordFailure("", err)
		r.log().Warn("switchboard reconciler is not fully configured", zap.Error(err))
		return
	}

	scope := registry.Scope{Namespace: r.namespace}
	pointer, err := r.registry.GetChannel(ctx, scope, r.channel)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			r.recordFailure("", err)
			r.log().Warn("failed to read switchboard channel", zap.String("namespace", r.namespace), zap.String("channel", r.channel), zap.Error(err))
		}
		return
	}
	r.updateState(func(state *ReconcilerState) {
		state.DesiredBundleID = pointer.BundleID
	})
	r.log().Info("switchboard desired bundle observed", zap.String("namespace", r.namespace), zap.String("channel", r.channel), zap.String("desired_bundle_id", pointer.BundleID))

	if current := r.manager.Current(); current != nil && current.ID() == pointer.BundleID {
		r.updateState(func(state *ReconcilerState) {
			state.ActiveBundleID = current.ID()
			state.LastError = ""
		})
		r.log().Debug("switchboard active bundle already current", zap.String("bundle_id", pointer.BundleID), zap.String("namespace", r.namespace), zap.String("channel", r.channel))
		return
	}

	r.log().Info("switchboard bundle download start", zap.String("bundle_id", pointer.BundleID), zap.String("namespace", r.namespace), zap.String("channel", r.channel))
	b, err := r.registry.GetBundle(ctx, scope, pointer.BundleID)
	if err != nil {
		r.recordFailure(pointer.BundleID, err)
		r.log().Warn("failed to read switchboard bundle", zap.String("bundle_id", pointer.BundleID), zap.Error(err))
		return
	}
	r.log().Info("switchboard bundle checksum verified", zap.String("bundle_id", pointer.BundleID), zap.String("checksum", b.Checksum))

	r.log().Info("switchboard bundle compile start", zap.String("bundle_id", pointer.BundleID), zap.Int("pool_size", r.effectivePoolSize()))
	candidate, err := NewRuntime(ctx, r.runtime, b, r.timeout, r.effectivePoolSize())
	if err != nil {
		r.recordFailure(pointer.BundleID, err)
		r.log().Warn("failed to compile or warm switchboard bundle", zap.String("bundle_id", pointer.BundleID), zap.Int("pool_size", r.effectivePoolSize()), zap.Error(err))
		return
	}
	r.log().Info("switchboard bundle compile and pool warm succeeded", zap.String("bundle_id", pointer.BundleID), zap.Int("pool_size", candidate.PoolSize()))

	r.log().Info("switchboard validation start", zap.String("bundle_id", pointer.BundleID))
	if err := candidate.Validate(ctx); err != nil {
		_ = candidate.Close(ctx)
		r.recordFailure(pointer.BundleID, err)
		r.log().Warn("switchboard validation failed", zap.String("bundle_id", pointer.BundleID), zap.Error(err))
		return
	}
	r.log().Info("switchboard validation succeeded", zap.String("bundle_id", pointer.BundleID))
	r.manager.Activate(candidate)
	r.recordSuccess(pointer.BundleID)
	r.log().Info("activated switchboard bundle", zap.String("bundle_id", pointer.BundleID), zap.String("namespace", r.namespace), zap.String("channel", r.channel))
}

func (r *Reconciler) effectivePoolSize() int {
	if r.poolSize <= 0 {
		return DefaultPoolSize
	}
	return r.poolSize
}

func (r *Reconciler) managerCurrent() *Runtime {
	if r.manager == nil {
		return nil
	}
	return r.manager.Current()
}

func (r *Reconciler) recordSuccess(bundleID string) {
	now := time.Now().UTC()
	activeID := ""
	if current := r.managerCurrent(); current != nil {
		activeID = current.ID()
	}
	r.updateState(func(state *ReconcilerState) {
		state.ActiveBundleID = activeID
		state.LastSuccessfulActivation = bundleID
		state.LastSuccessAt = now
		state.LastError = ""
	})
}

func (r *Reconciler) recordFailure(bundleID string, err error) {
	now := time.Now().UTC()
	activeID := ""
	if current := r.managerCurrent(); current != nil {
		activeID = current.ID()
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	r.updateState(func(state *ReconcilerState) {
		state.ActiveBundleID = activeID
		if bundleID != "" {
			state.LastFailedActivation = bundleID
		}
		state.LastFailureAt = now
		state.LastError = errText
	})
}

func (r *Reconciler) updateState(fn func(*ReconcilerState)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.state)
}

func (r *Reconciler) log() *zap.Logger {
	if r.logger == nil {
		return zap.NewNop()
	}
	return r.logger
}
