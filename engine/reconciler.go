package engine

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/internal/bundlecache"
	"github.com/ethndotsh/switchboard/registry"
	"go.uber.org/zap"
)

const (
	backoffBase = time.Second
	backoffCap  = 5 * time.Minute
)

type ReconcilerState struct {
	Namespace                string    `json:"namespace,omitempty"`
	Channel                  string    `json:"channel"`
	ActiveBundleID           string    `json:"active_bundle_id,omitempty"`
	DesiredBundleID          string    `json:"desired_bundle_id,omitempty"`
	LastSuccessfulActivation string    `json:"last_successful_activation,omitempty"`
	LastFailedActivation     string    `json:"last_failed_activation,omitempty"`
	LastError                string    `json:"last_error,omitempty"`
	LastTestReport           string    `json:"last_test_report,omitempty"`
	QuarantinedBundleID      string    `json:"quarantined_bundle_id,omitempty"`
	QuarantineReason         string    `json:"quarantine_reason,omitempty"`
	NextRetryAt              time.Time `json:"next_retry_at,omitempty"`
	TransientFailures        int       `json:"transient_failures,omitempty"`
	ActivationsSucceeded     int64     `json:"activations_succeeded"`
	ActivationsFailed        int64     `json:"activations_failed"`
	LastCheckedAt            time.Time `json:"last_checked_at,omitempty"`
	LastSuccessAt            time.Time `json:"last_success_at,omitempty"`
	LastFailureAt            time.Time `json:"last_failure_at,omitempty"`
}

type quarantineKey struct {
	BundleID string
	Checksum string
}

type Reconciler struct {
	registry  registry.Registry
	manager   *RuntimeManager
	runtime   WasmRuntime
	cache     *bundlecache.Cache
	namespace string
	channel   string
	interval  time.Duration
	limits    InvokeLimits
	poolCfg   PoolConfig
	logger    *zap.Logger

	quarantine      quarantineKey
	backoffAttempts int
	nextRetryAt     time.Time
	rng             *rand.Rand

	mu    sync.RWMutex
	state ReconcilerState
}

type Watcher = Reconciler

func NewReconciler(reg registry.Registry, manager *RuntimeManager, wasmRuntime WasmRuntime, cfg ResolvedConfig, logger *zap.Logger) *Reconciler {
	return &Reconciler{
		registry:  reg,
		manager:   manager,
		runtime:   wasmRuntime,
		namespace: cfg.Namespace,
		channel:   cfg.Channel,
		interval:  cfg.PollInterval,
		limits:    cfg.invokeLimits(),
		poolCfg: PoolConfig{
			MinSize:   cfg.MinPoolSize,
			MaxSize:   cfg.MaxPoolSize,
			Autoscale: cfg.PoolAutoscale,
		},
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
		logger: logger,
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

	if r.registry == nil || r.manager == nil || r.runtime == nil {
		err := errors.New("switchboard reconciler is not fully configured")
		r.recordFailure("", err)
		r.log().Warn("switchboard reconciler is not fully configured", zap.Error(err))
		return
	}

	if !r.nextRetryAt.IsZero() && time.Now().Before(r.nextRetryAt) {
		r.log().Debug("switchboard reconcile backing off", zap.Time("next_retry_at", r.nextRetryAt))
		return
	}

	scope := registry.Scope{Namespace: r.namespace}
	pointer, err := r.registry.GetChannel(ctx, scope, r.channel)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			r.recordTransientFailure("", err)
			r.log().Warn("failed to read switchboard channel", zap.String("namespace", r.namespace), zap.String("channel", r.channel), zap.Error(err))
		}
		return
	}
	r.resetBackoff()
	r.updateState(func(state *ReconcilerState) {
		state.DesiredBundleID = pointer.BundleID
	})

	if current := r.manager.Current(); current != nil && current.ID() == pointer.BundleID {
		r.updateState(func(state *ReconcilerState) {
			state.ActiveBundleID = current.ID()
			state.LastError = ""
		})
		r.log().Debug("switchboard active bundle already current", zap.String("bundle_id", pointer.BundleID), zap.String("namespace", r.namespace), zap.String("channel", r.channel))
		return
	}

	if (quarantineKey{pointer.BundleID, pointer.Checksum}) == r.quarantine && r.quarantine != (quarantineKey{}) {
		r.log().Debug("switchboard bundle quarantined; waiting for a new channel pointer",
			zap.String("bundle_id", pointer.BundleID), zap.String("namespace", r.namespace), zap.String("channel", r.channel))
		return
	}

	r.log().Info("switchboard bundle download start", zap.String("bundle_id", pointer.BundleID), zap.String("namespace", r.namespace), zap.String("channel", r.channel))
	b, err := r.registry.GetBundle(ctx, scope, pointer.BundleID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if errors.Is(err, bundle.ErrInvalid) {
			r.quarantineBundle(pointer, err)
		} else {
			r.recordTransientFailure(pointer.BundleID, err)
		}
		r.log().Warn("failed to read switchboard bundle", zap.String("bundle_id", pointer.BundleID), zap.Error(err))
		return
	}
	r.log().Info("switchboard bundle verified", zap.String("bundle_id", pointer.BundleID), zap.String("checksum", b.Checksum))

	poolCfg := r.effectivePoolConfig()
	r.log().Info("switchboard bundle compile start", zap.String("bundle_id", pointer.BundleID), zap.Int("min_pool_size", poolCfg.MinSize), zap.Int("max_pool_size", poolCfg.MaxSize), zap.Bool("pool_autoscale", poolCfg.Autoscale))
	candidate, err := NewRuntimeWithPoolConfig(ctx, r.runtime, b, r.limits, poolCfg, r.logger)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		r.quarantineBundle(pointer, err)
		r.log().Warn("failed to compile or warm switchboard bundle", zap.String("bundle_id", pointer.BundleID), zap.Error(err))
		return
	}

	r.log().Info("switchboard validation start", zap.String("bundle_id", pointer.BundleID))
	if err := candidate.Validate(ctx); err != nil {
		_ = candidate.Close(ctx)
		if errors.Is(err, context.Canceled) {
			return
		}
		r.quarantineBundle(pointer, err)
		r.log().Warn("switchboard validation failed", zap.String("bundle_id", pointer.BundleID), zap.Error(err))
		return
	}

	if err := r.runEmbeddedTests(ctx, candidate, b); err != nil {
		_ = candidate.Close(ctx)
		if errors.Is(err, context.Canceled) {
			return
		}
		r.quarantineBundle(pointer, err)
		r.log().Warn("switchboard embedded test suite failed", zap.String("bundle_id", pointer.BundleID), zap.Error(err))
		return
	}

	r.manager.Activate(candidate)
	r.recordSuccess(pointer.BundleID)
	r.persistBundle(pointer, b)
	r.log().Info("activated switchboard bundle", zap.String("bundle_id", pointer.BundleID), zap.String("namespace", r.namespace), zap.String("channel", r.channel))
}

func (r *Reconciler) runEmbeddedTests(ctx context.Context, candidate *Runtime, b bundle.Bundle) error {
	if len(b.Tests) == 0 {
		return nil
	}
	return r.runTestSuite(ctx, candidate, b)
}

func (r *Reconciler) persistBundle(pointer bundle.ChannelPointer, b bundle.Bundle) {
	if r.cache == nil {
		return
	}
	meta := bundlecache.Metadata{
		BundleID:    pointer.BundleID,
		Checksum:    b.Checksum,
		Namespace:   r.namespace,
		Channel:     r.channel,
		ActivatedAt: time.Now().UTC(),
	}
	if err := r.cache.Store(r.namespace, r.channel, b, meta); err != nil {
		r.log().Warn("failed to persist switchboard bundle cache", zap.String("bundle_id", pointer.BundleID), zap.Error(err))
	}
}

func (r *Reconciler) effectivePoolConfig() PoolConfig {
	cfg := r.poolCfg
	if cfg.MinSize <= 0 {
		cfg.MinSize = DefaultPoolSize
	}
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = cfg.MinSize
	}
	if cfg.MaxSize < cfg.MinSize {
		cfg.MaxSize = cfg.MinSize
	}
	return cfg
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
	r.resetBackoff()
	r.quarantine = quarantineKey{}
	r.updateState(func(state *ReconcilerState) {
		state.ActiveBundleID = activeID
		state.LastSuccessfulActivation = bundleID
		state.LastSuccessAt = now
		state.LastError = ""
		state.QuarantinedBundleID = ""
		state.QuarantineReason = ""
		state.ActivationsSucceeded++
	})
}

func (r *Reconciler) resetBackoff() {
	r.backoffAttempts = 0
	r.nextRetryAt = time.Time{}
	r.updateState(func(state *ReconcilerState) {
		state.TransientFailures = 0
		state.NextRetryAt = time.Time{}
	})
}

func (r *Reconciler) recordTransientFailure(bundleID string, err error) {
	r.backoffAttempts++
	delay := backoffDelay(r.backoffAttempts, r.rng)
	r.nextRetryAt = time.Now().Add(delay)
	nextRetry := r.nextRetryAt
	attempts := r.backoffAttempts
	r.recordFailure(bundleID, err)
	r.updateState(func(state *ReconcilerState) {
		state.TransientFailures = attempts
		state.NextRetryAt = nextRetry
	})
	r.log().Warn("switchboard reconcile transient failure",
		zap.String("bundle_id", bundleID),
		zap.Int("attempts", attempts),
		zap.Duration("retry_in", delay),
		zap.Error(err))
}

// quarantineBundle marks a bundle permanently failed for the current channel
// pointer; it is not retried until the pointer changes.
func (r *Reconciler) quarantineBundle(pointer bundle.ChannelPointer, err error) {
	r.quarantine = quarantineKey{BundleID: pointer.BundleID, Checksum: pointer.Checksum}
	r.recordFailure(pointer.BundleID, err)
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	r.updateState(func(state *ReconcilerState) {
		state.QuarantinedBundleID = pointer.BundleID
		state.QuarantineReason = errText
	})
	r.log().Warn("quarantining switchboard bundle until channel pointer changes",
		zap.String("bundle_id", pointer.BundleID),
		zap.String("namespace", r.namespace),
		zap.String("channel", r.channel),
		zap.Error(err))
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
		state.ActivationsFailed++
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

func backoffDelay(attempts int, rng *rand.Rand) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := backoffBase << uint(minInt(attempts-1, 20))
	if delay > backoffCap || delay <= 0 {
		delay = backoffCap
	}
	jitter := 0.8 + 0.4*rng.Float64()
	return time.Duration(float64(delay) * jitter)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
