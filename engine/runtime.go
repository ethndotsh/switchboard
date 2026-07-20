package engine

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/engine/wasmapi"
	"github.com/ethndotsh/switchboard/internal/bundle"
	"go.uber.org/zap"
)

const DefaultPoolSize = 16

const (
	DefaultInvokeTimeout   = 50 * time.Millisecond
	DefaultMaxActionBytes  = 64 << 10
	DefaultMaxHeaderOps    = 32
	DefaultMaxResponseBody = 8 << 10
	DefaultMaxDataBytes    = 4 << 20
)

const (
	retireGracePeriod  = 15 * time.Second
	retirePollInterval = 25 * time.Millisecond
)

type WasmRuntime = wasmapi.WasmRuntime
type CompiledRule = wasmapi.CompiledRule
type RuleInstance = wasmapi.RuleInstance
type InvokeLimits = wasmapi.InvokeLimits

type Runtime struct {
	id          string
	manifest    bundle.Manifest
	wasmRuntime WasmRuntime
	module      CompiledRule
	limits      InvokeLimits
	pool        chan RuleInstance
	closed      atomic.Bool
	logger      *zap.Logger

	minPoolSize       int
	maxPoolSize       int
	autoscale         bool
	scaleInterval     time.Duration
	idleWindowLimit   int
	idleWindows       int
	scaleCancel       context.CancelFunc
	targetPoolSize    atomic.Int64
	totalInstances    atomic.Int64
	inflight          atomic.Int64
	inflightHighWater atomic.Int64
	exhaustions       atomic.Int64
	exhaustionsTotal  atomic.Int64
}

type RuntimeManager struct {
	active   atomic.Pointer[Runtime]
	lastGood atomic.Pointer[Runtime]

	logger   *zap.Logger
	stopInit sync.Once
	stopOnce sync.Once
	stop     chan struct{}
	retireWG sync.WaitGroup
}

func (m *RuntimeManager) Current() *Runtime {
	return m.active.Load()
}

func (m *RuntimeManager) LastGood() *Runtime {
	return m.lastGood.Load()
}

func (m *RuntimeManager) TryActivate(ctx context.Context, candidate *Runtime) error {
	if err := candidate.Validate(ctx); err != nil {
		return err
	}
	m.Activate(candidate)
	return nil
}

func (m *RuntimeManager) Activate(candidate *Runtime) {
	previous := m.active.Swap(candidate)
	if previous == nil || previous == candidate {
		return
	}
	displaced := m.lastGood.Swap(previous)
	if displaced != nil && displaced != previous && displaced != candidate {
		m.retire(displaced)
	}
}

// retire closes a displaced runtime once its in-flight invocations drain or
// the grace period expires.
func (m *RuntimeManager) retire(r *Runtime) {
	stop := m.stopCh()
	m.retireWG.Add(1)
	go func() {
		defer m.retireWG.Done()
		deadline := time.NewTimer(retireGracePeriod)
		defer deadline.Stop()
		ticker := time.NewTicker(retirePollInterval)
		defer ticker.Stop()
	wait:
		for r.Inflight() > 0 && !r.IsClosed() {
			select {
			case <-stop:
				break wait
			case <-deadline.C:
				break wait
			case <-ticker.C:
			}
		}
		remaining := r.Inflight()
		_ = r.Close(context.Background())
		if m.logger != nil {
			m.logger.Info("retired switchboard runtime",
				zap.String("bundle_id", r.ID()),
				zap.Int64("inflight_at_close", remaining),
			)
		}
	}()
}

func (m *RuntimeManager) stopCh() chan struct{} {
	m.stopInit.Do(func() {
		m.stop = make(chan struct{})
	})
	return m.stop
}

func (m *RuntimeManager) Close(ctx context.Context) error {
	m.stopOnce.Do(func() {
		close(m.stopCh())
	})
	m.retireWG.Wait()
	seen := map[*Runtime]bool{}
	for _, runtime := range []*Runtime{m.active.Load(), m.lastGood.Load()} {
		if runtime == nil || seen[runtime] {
			continue
		}
		seen[runtime] = true
		if err := runtime.Close(ctx); err != nil {
			return err
		}
	}
	return nil
}

func NewRuntime(ctx context.Context, wasmRuntime WasmRuntime, b bundle.Bundle, limits InvokeLimits, poolSize int) (*Runtime, error) {
	return NewRuntimeWithPoolConfig(ctx, wasmRuntime, b, limits, PoolConfig{MinSize: poolSize, MaxSize: poolSize, Autoscale: false}, nil)
}

func NewRuntimeWithPoolConfig(ctx context.Context, wasmRuntime WasmRuntime, b bundle.Bundle, limits InvokeLimits, poolCfg PoolConfig, logger *zap.Logger) (*Runtime, error) {
	limits = resolveInvokeLimits(limits)
	minPoolSize := poolCfg.MinSize
	if minPoolSize <= 0 {
		minPoolSize = DefaultPoolSize
	}
	maxPoolSize := poolCfg.MaxSize
	if maxPoolSize <= 0 {
		maxPoolSize = minPoolSize
	}
	if maxPoolSize < minPoolSize {
		maxPoolSize = minPoolSize
	}
	if err := checkDataSize(b.Data, limits.MaxDataBytes); err != nil {
		return nil, err
	}
	compiled, err := wasmRuntime.Compile(ctx, b.Module, b.Data)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		id:          b.ID,
		manifest:    b.Manifest,
		wasmRuntime: wasmRuntime,
		module:      compiled,
		limits:      limits,
		pool:        make(chan RuleInstance, maxPoolSize),
		logger:      logger,
		minPoolSize: minPoolSize,
		maxPoolSize: maxPoolSize,
		autoscale:   poolCfg.Autoscale && maxPoolSize > minPoolSize,

		scaleInterval:   defaultPoolScaleInterval,
		idleWindowLimit: defaultPoolIdleWindows,
	}
	runtime.targetPoolSize.Store(int64(minPoolSize))
	if err := runtime.Warm(ctx, minPoolSize); err != nil {
		_ = runtime.Close(ctx)
		return nil, err
	}
	if runtime.autoscale {
		scaleCtx, cancel := context.WithCancel(context.Background())
		runtime.scaleCancel = cancel
		go runtime.runPoolController(scaleCtx)
	}
	return runtime, nil
}

// checkDataSize rejects a bundle whose data artifacts exceed the configured
// cap; returning an ErrInvalid keeps the reconciler quarantining it rather
// than retrying.
func checkDataSize(data map[string][]byte, limit int) error {
	if limit <= 0 {
		return nil
	}
	total := 0
	for _, value := range data {
		total += len(value)
	}
	if total > limit {
		return fmt.Errorf("%w: bundle data %d bytes exceeds max_data_bytes %d", bundle.ErrInvalid, total, limit)
	}
	return nil
}

func resolveInvokeLimits(limits InvokeLimits) InvokeLimits {
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultInvokeTimeout
	}
	if limits.MaxActionBytes <= 0 {
		limits.MaxActionBytes = DefaultMaxActionBytes
	}
	if limits.MaxHeaderOps <= 0 {
		limits.MaxHeaderOps = DefaultMaxHeaderOps
	}
	if limits.MaxResponseBody <= 0 {
		limits.MaxResponseBody = DefaultMaxResponseBody
	}
	if limits.MaxDataBytes <= 0 {
		limits.MaxDataBytes = DefaultMaxDataBytes
	}
	return limits
}

func (r *Runtime) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

func (r *Runtime) Inflight() int64 {
	if r == nil {
		return 0
	}
	return r.inflight.Load()
}

func (r *Runtime) IsClosed() bool {
	if r == nil {
		return true
	}
	return r.closed.Load()
}

func (r *Runtime) Validate(ctx context.Context) error {
	_, err := r.Invoke(ctx, switchboard.Request{
		Method:  http.MethodGet,
		Scheme:  "http",
		Host:    "switchboard.validate",
		Path:    "/__switchboard_validate",
		Headers: map[string][]string{},
	})
	return err
}

func (r *Runtime) Invoke(ctx context.Context, req switchboard.Request) (switchboard.Action, error) {
	mod, err := r.acquireModule(ctx)
	if err != nil {
		return switchboard.Action{}, err
	}
	healthy := false
	defer func() {
		r.releaseModule(context.Background(), mod, healthy)
	}()

	// With CloseOnContextDone, a timed-out guest's module instance is
	// destroyed; never mark an errored instance healthy for reuse.
	action, err := mod.Invoke(ctx, r.manifest.Entrypoint, req, r.limits)
	if err != nil {
		return switchboard.Action{}, err
	}
	healthy = true
	if action.Decision == "" {
		action.Decision = switchboard.DecisionNext
	}
	return action, nil
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil || r.closed.Swap(true) {
		return nil
	}
	if r.scaleCancel != nil {
		r.scaleCancel()
	}
	for {
		select {
		case mod := <-r.pool:
			_ = mod.Close(ctx)
			r.totalInstances.Add(-1)
		default:
			return r.module.Close(ctx)
		}
	}
}
