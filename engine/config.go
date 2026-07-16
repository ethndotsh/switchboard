package engine

import (
	"fmt"
	"time"

	"github.com/ethndotsh/switchboard/registry"
)

const (
	FailModeOpen     = "open"
	FailModeClosed   = "closed"
	FailModeLastGood = "last_good"
)

type Config struct {
	Registry           string
	RegistryURL        string
	Namespace          string
	Channel            string
	PollInterval       string
	FailMode           string
	FallbackFailMode   string
	InvokeTimeout      string
	MemoryLimit        string
	MaxActionBytes     string
	MaxHeaderOps       int
	MaxResponseBody    string
	CacheDir           string
	BootstrapFromCache string
	PoolAutoscale      string
	PoolSize           int
	MinPoolSize        int
	MaxPoolSize        int
}

type ResolvedConfig struct {
	Registry           string
	RegistryURL        string
	Namespace          string
	Channel            string
	FailMode           string
	FallbackFailMode   string
	PollInterval       time.Duration
	InvokeTimeout      time.Duration
	MemoryLimitBytes   int64
	MaxActionBytes     int
	MaxHeaderOps       int
	MaxResponseBody    int
	CacheDir           string
	BootstrapFromCache bool
	PoolAutoscale      bool
	PoolSize           int
	MinPoolSize        int
	MaxPoolSize        int
}

func (c ResolvedConfig) invokeLimits() InvokeLimits {
	return InvokeLimits{
		Timeout:         c.InvokeTimeout,
		MaxActionBytes:  c.MaxActionBytes,
		MaxHeaderOps:    c.MaxHeaderOps,
		MaxResponseBody: c.MaxResponseBody,
	}
}

func ResolveConfig(cfg Config) (ResolvedConfig, error) {
	resolved := ResolvedConfig{
		Registry:         cfg.Registry,
		RegistryURL:      cfg.RegistryURL,
		Namespace:        cfg.Namespace,
		Channel:          cfg.Channel,
		FailMode:         cfg.FailMode,
		FallbackFailMode: cfg.FallbackFailMode,
		PollInterval:     2 * time.Second,
		InvokeTimeout:    DefaultInvokeTimeout,
		MemoryLimitBytes: 32 << 20,
		MaxActionBytes:   DefaultMaxActionBytes,
		MaxHeaderOps:     DefaultMaxHeaderOps,
		MaxResponseBody:  DefaultMaxResponseBody,
		CacheDir:         cfg.CacheDir,
		PoolAutoscale:    true,
		PoolSize:         cfg.PoolSize,
	}
	if resolved.Channel == "" {
		resolved.Channel = "prod"
	}
	if err := registry.ValidateNamespace(resolved.Namespace); err != nil {
		return ResolvedConfig{}, err
	}
	switch resolved.FailMode {
	case "":
		resolved.FailMode = FailModeOpen
	case FailModeOpen, FailModeClosed, FailModeLastGood:
	default:
		return ResolvedConfig{}, fmt.Errorf("invalid fail_mode %q; expected open, closed, or last_good", resolved.FailMode)
	}
	switch resolved.FallbackFailMode {
	case "":
		resolved.FallbackFailMode = FailModeOpen
	case FailModeOpen, FailModeClosed:
		if resolved.FailMode != FailModeLastGood {
			return ResolvedConfig{}, fmt.Errorf("fallback_fail_mode requires fail_mode last_good")
		}
	default:
		return ResolvedConfig{}, fmt.Errorf("invalid fallback_fail_mode %q; expected open or closed", resolved.FallbackFailMode)
	}
	if cfg.MemoryLimit != "" {
		limit, err := parseByteSize(cfg.MemoryLimit)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("invalid memory_limit: %w", err)
		}
		if limit < 1<<20 {
			return ResolvedConfig{}, fmt.Errorf("memory_limit must be at least 1mb")
		}
		if limit > 4<<30 {
			return ResolvedConfig{}, fmt.Errorf("memory_limit must be at most 4gb")
		}
		resolved.MemoryLimitBytes = limit
	}
	if cfg.MaxActionBytes != "" {
		limit, err := parseByteSize(cfg.MaxActionBytes)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("invalid max_action_bytes: %w", err)
		}
		if limit > 1<<20 {
			return ResolvedConfig{}, fmt.Errorf("max_action_bytes must be at most 1mb")
		}
		resolved.MaxActionBytes = int(limit)
	}
	if cfg.MaxHeaderOps < 0 || cfg.MaxHeaderOps > 1024 {
		return ResolvedConfig{}, fmt.Errorf("max_header_ops must be between 1 and 1024")
	}
	if cfg.MaxHeaderOps > 0 {
		resolved.MaxHeaderOps = cfg.MaxHeaderOps
	}
	if cfg.MaxResponseBody != "" {
		limit, err := parseByteSize(cfg.MaxResponseBody)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("invalid max_response_body: %w", err)
		}
		if limit > 1<<20 {
			return ResolvedConfig{}, fmt.Errorf("max_response_body must be at most 1mb")
		}
		resolved.MaxResponseBody = int(limit)
	}
	switch cfg.BootstrapFromCache {
	case "":
		resolved.BootstrapFromCache = resolved.CacheDir != ""
	case "on", "true":
		if resolved.CacheDir == "" {
			return ResolvedConfig{}, fmt.Errorf("bootstrap_from_cache requires cache_dir")
		}
		resolved.BootstrapFromCache = true
	case "off", "false":
		resolved.BootstrapFromCache = false
	default:
		return ResolvedConfig{}, fmt.Errorf("invalid bootstrap_from_cache %q; expected on/off/true/false", cfg.BootstrapFromCache)
	}
	autoscale, err := parsePoolAutoscale(cfg.PoolAutoscale)
	if err != nil {
		return ResolvedConfig{}, err
	}
	resolved.PoolAutoscale = autoscale
	if cfg.PoolSize < 0 {
		return ResolvedConfig{}, fmt.Errorf("pool_size must be greater than zero")
	}
	if cfg.MinPoolSize < 0 {
		return ResolvedConfig{}, fmt.Errorf("min_pool_size must be greater than zero")
	}
	if cfg.MaxPoolSize < 0 {
		return ResolvedConfig{}, fmt.Errorf("max_pool_size must be greater than zero")
	}
	if cfg.MinPoolSize > 0 {
		resolved.MinPoolSize = cfg.MinPoolSize
	} else if cfg.PoolSize > 0 {
		resolved.MinPoolSize = cfg.PoolSize
	} else {
		resolved.MinPoolSize = DefaultPoolSize
	}
	resolved.PoolSize = resolved.MinPoolSize
	if cfg.MaxPoolSize > 0 {
		resolved.MaxPoolSize = cfg.MaxPoolSize
	} else {
		resolved.MaxPoolSize = resolved.MinPoolSize * 4
		if resolved.MaxPoolSize > DefaultMaxPoolSize {
			resolved.MaxPoolSize = DefaultMaxPoolSize
		}
	}
	if !resolved.PoolAutoscale {
		resolved.MaxPoolSize = resolved.MinPoolSize
	}
	if resolved.MaxPoolSize < resolved.MinPoolSize {
		return ResolvedConfig{}, fmt.Errorf("max_pool_size must be greater than or equal to min_pool_size")
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
	switch resolved.Registry {
	case "", "s3", "file", "https":
	default:
		return ResolvedConfig{}, fmt.Errorf("unsupported registry %q; expected s3, file, or https", resolved.Registry)
	}
	return resolved, nil
}

func parsePoolAutoscale(value string) (bool, error) {
	switch value {
	case "", "on", "true":
		return true, nil
	case "off", "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid pool_autoscale %q; expected on/off/true/false", value)
	}
}
