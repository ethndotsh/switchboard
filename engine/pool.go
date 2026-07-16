package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

const DefaultMaxPoolSize = 128

const (
	defaultPoolScaleInterval = time.Second
	defaultPoolIdleWindows   = 30
)

var ErrRuntimePoolExhausted = errors.New("switchboard runtime pool exhausted")

type PoolConfig struct {
	MinSize   int
	MaxSize   int
	Autoscale bool
}

func (r *Runtime) PoolSize() int {
	if r == nil || r.pool == nil {
		return 0
	}
	return int(r.targetPoolSize.Load())
}

func (r *Runtime) PoolBounds() (int, int) {
	if r == nil {
		return 0, 0
	}
	return r.minPoolSize, r.maxPoolSize
}

func (r *Runtime) Stats() PoolStats {
	if r == nil {
		return PoolStats{}
	}
	return PoolStats{
		MinSize:        r.minPoolSize,
		MaxSize:        r.maxPoolSize,
		TargetSize:     int(r.targetPoolSize.Load()),
		TotalInstances: r.totalInstances.Load(),
		Inflight:       r.inflight.Load(),
		Exhaustions:    r.exhaustionsTotal.Load(),
	}
}

func (r *Runtime) Warm(ctx context.Context, count int) error {
	if r.closed.Load() {
		return fmt.Errorf("runtime %s is closed", r.id)
	}
	for i := 0; i < count; i++ {
		mod, err := r.module.Instantiate(ctx)
		if err != nil {
			return err
		}
		select {
		case r.pool <- mod:
			r.totalInstances.Add(1)
		default:
			_ = mod.Close(ctx)
			return nil
		}
	}
	return nil
}

func (r *Runtime) acquireModule(ctx context.Context) (RuleInstance, error) {
	if r.closed.Load() {
		return nil, fmt.Errorf("runtime %s is closed", r.id)
	}
	select {
	case mod := <-r.pool:
		current := r.inflight.Add(1)
		r.recordInflightHighWater(current)
		return mod, nil
	default:
		r.exhaustions.Add(1)
		r.exhaustionsTotal.Add(1)
		r.log().Warn("switchboard runtime pool exhausted",
			zap.String("bundle_id", r.id),
			zap.Int("target_pool_size", int(r.targetPoolSize.Load())),
			zap.Int("min_pool_size", r.minPoolSize),
			zap.Int("max_pool_size", r.maxPoolSize),
			zap.Int("inflight", int(r.inflight.Load())),
		)
		return nil, ErrRuntimePoolExhausted
	}
}

func (r *Runtime) releaseModule(ctx context.Context, mod RuleInstance, healthy bool) {
	if mod == nil {
		return
	}
	r.inflight.Add(-1)
	if !healthy || r.closed.Load() {
		_ = mod.Close(ctx)
		r.totalInstances.Add(-1)
		// Replace discarded instances synchronously; with autoscale off
		// nothing else re-warms the pool and it would drain permanently.
		if !r.closed.Load() && r.totalInstances.Load() < r.targetPoolSize.Load() {
			replacement, err := r.module.Instantiate(ctx)
			if err != nil {
				r.log().Warn("failed to replace unhealthy switchboard pool instance", zap.String("bundle_id", r.id), zap.Error(err))
				return
			}
			select {
			case r.pool <- replacement:
				r.totalInstances.Add(1)
			default:
				_ = replacement.Close(ctx)
			}
		}
		return
	}
	if r.totalInstances.Load() > r.targetPoolSize.Load() {
		_ = mod.Close(ctx)
		r.totalInstances.Add(-1)
		r.log().Debug("switchboard runtime pool instance retired", zap.String("bundle_id", r.id), zap.Int("target_pool_size", int(r.targetPoolSize.Load())))
		return
	}
	select {
	case r.pool <- mod:
	default:
		_ = mod.Close(ctx)
		r.totalInstances.Add(-1)
	}
}

func (r *Runtime) runPoolController(ctx context.Context) {
	interval := r.scaleInterval
	if interval <= 0 {
		interval = defaultPoolScaleInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.adjustPool(ctx)
		}
	}
}

func (r *Runtime) adjustPool(ctx context.Context) {
	if r.closed.Load() {
		return
	}
	target := int(r.targetPoolSize.Load())
	exhaustions := r.exhaustions.Swap(0)
	highWater := int(r.inflightHighWater.Swap(0))

	if exhaustions > 0 || highWater*100 >= target*80 {
		next := target + maxInt(1, target/4)
		if next > r.maxPoolSize {
			next = r.maxPoolSize
		}
		if next != target {
			r.targetPoolSize.Store(int64(next))
			r.idleWindows = 0
			r.log().Info("switchboard runtime pool scaled up",
				zap.String("bundle_id", r.id),
				zap.Int("previous_target_pool_size", target),
				zap.Int("target_pool_size", next),
				zap.Int("min_pool_size", r.minPoolSize),
				zap.Int("max_pool_size", r.maxPoolSize),
				zap.Int64("pool_exhaustions", exhaustions),
				zap.Int("inflight_high_water", highWater),
			)
		}
		r.warmToTarget(ctx)
		return
	}

	if highWater*100 < target*50 && target > r.minPoolSize {
		r.idleWindows++
	} else {
		r.idleWindows = 0
	}
	if r.idleWindows >= r.idleWindowLimit {
		next := target - maxInt(1, target/10)
		if next < r.minPoolSize {
			next = r.minPoolSize
		}
		if next != target {
			r.targetPoolSize.Store(int64(next))
			r.log().Info("switchboard runtime pool scaled down",
				zap.String("bundle_id", r.id),
				zap.Int("previous_target_pool_size", target),
				zap.Int("target_pool_size", next),
				zap.Int("min_pool_size", r.minPoolSize),
				zap.Int("max_pool_size", r.maxPoolSize),
				zap.Int("inflight_high_water", highWater),
			)
		}
		r.idleWindows = 0
	}
	r.retireIdleExcess(ctx)
}

func (r *Runtime) warmToTarget(ctx context.Context) {
	for !r.closed.Load() && int(r.totalInstances.Load()) < int(r.targetPoolSize.Load()) {
		mod, err := r.module.Instantiate(ctx)
		if err != nil {
			r.log().Warn("failed to warm switchboard runtime pool instance", zap.String("bundle_id", r.id), zap.Error(err))
			return
		}
		select {
		case r.pool <- mod:
			r.totalInstances.Add(1)
			r.log().Debug("switchboard runtime pool instance warmed", zap.String("bundle_id", r.id), zap.Int("target_pool_size", int(r.targetPoolSize.Load())), zap.Int("total_instances", int(r.totalInstances.Load())))
		default:
			_ = mod.Close(ctx)
			return
		}
	}
}

func (r *Runtime) retireIdleExcess(ctx context.Context) {
	for int(r.totalInstances.Load()) > int(r.targetPoolSize.Load()) {
		select {
		case mod := <-r.pool:
			_ = mod.Close(ctx)
			r.totalInstances.Add(-1)
			r.log().Debug("switchboard runtime pool idle instance retired", zap.String("bundle_id", r.id), zap.Int("target_pool_size", int(r.targetPoolSize.Load())), zap.Int("total_instances", int(r.totalInstances.Load())))
		default:
			return
		}
	}
}

func (r *Runtime) recordInflightHighWater(current int64) {
	for {
		previous := r.inflightHighWater.Load()
		if current <= previous || r.inflightHighWater.CompareAndSwap(previous, current) {
			return
		}
	}
}

func (r *Runtime) log() *zap.Logger {
	if r.logger == nil {
		return zap.NewNop()
	}
	return r.logger
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
