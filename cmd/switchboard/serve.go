package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	httpadapter "github.com/ethndotsh/switchboard/adapters/http"
	"github.com/ethndotsh/switchboard/engine"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

func serveCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "listen", "upstream", "registry", "namespace", "channel",
		"status-listen", "fail-mode", "fallback-fail-mode", "poll-interval", "invoke-timeout",
		"memory-limit", "max-action-bytes", "max-header-ops", "max-response-body", "max-data-bytes",
		"cache-dir", "bootstrap-from-cache", "pool-autoscale", "pool-size", "min-pool-size", "max-pool-size")
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", ":8080", "listen address")
	upstream := fs.String("upstream", "", "upstream URL or host:port to proxy to")
	registryURL := fs.String("registry", cfg.Registry, "registry URL, e.g. file://./registry")
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	channel := fs.String("channel", cfg.Channel, "channel name")
	statusListen := fs.String("status-listen", "", "separate listen address for /switchboard/status and /metrics (default: same listener)")
	failMode := fs.String("fail-mode", "open", "fail mode: open, closed, or last_good")
	fallbackFailMode := fs.String("fallback-fail-mode", "", "fallback fail mode when fail-mode is last_good: open or closed")
	pollInterval := fs.String("poll-interval", "", "registry poll interval (default 2s)")
	invokeTimeout := fs.String("invoke-timeout", "", "invocation timeout (default 50ms)")
	memoryLimit := fs.String("memory-limit", "", "guest memory limit (default 32mb)")
	maxActionBytes := fs.String("max-action-bytes", "", "maximum encoded action size (default 64kb)")
	maxHeaderOps := fs.Int("max-header-ops", 0, "maximum header operations per action (default 32)")
	maxResponseBody := fs.String("max-response-body", "", "maximum respond body size (default 8kb)")
	maxDataBytes := fs.String("max-data-bytes", "", "maximum total bundle data size (default 4mb)")
	cacheDir := fs.String("cache-dir", "", "durable last-known-good cache directory")
	bootstrapFromCache := fs.String("bootstrap-from-cache", "", "activate the cached bundle before the registry: on/off (default on with cache-dir)")
	poolAutoscale := fs.String("pool-autoscale", "", "autoscale the instance pool: on/off (default on)")
	poolSize := fs.Int("pool-size", 0, "instance pool size (default 16)")
	minPoolSize := fs.Int("min-pool-size", 0, "minimum instance pool size")
	maxPoolSize := fs.Int("max-pool-size", 0, "maximum instance pool size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *upstream == "" {
		return errors.New("usage: switchboard serve --listen :8080 --upstream localhost:3000 --registry file://./registry --channel dev")
	}
	upstreamURL, err := parseUpstream(*upstream)
	if err != nil {
		return err
	}

	logger, err := zap.NewProduction()
	if err != nil {
		return err
	}
	defer logger.Sync()

	service, err := engine.Start(ctx, engine.Config{
		RegistryURL:        *registryURL,
		Namespace:          *namespace,
		Channel:            *channel,
		PollInterval:       *pollInterval,
		FailMode:           *failMode,
		FallbackFailMode:   *fallbackFailMode,
		InvokeTimeout:      *invokeTimeout,
		MemoryLimit:        *memoryLimit,
		MaxActionBytes:     *maxActionBytes,
		MaxHeaderOps:       *maxHeaderOps,
		MaxResponseBody:    *maxResponseBody,
		MaxDataBytes:       *maxDataBytes,
		CacheDir:           *cacheDir,
		BootstrapFromCache: *bootstrapFromCache,
		PoolAutoscale:      *poolAutoscale,
		PoolSize:           *poolSize,
		MinPoolSize:        *minPoolSize,
		MaxPoolSize:        *maxPoolSize,
	}, logger)
	if err != nil {
		return err
	}
	defer service.Close(context.Background())

	promRegistry := prometheus.NewRegistry()
	metrics, err := httpadapter.NewMetrics(promRegistry, service)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	handler := httpadapter.Middleware(service, httpadapter.Options{
		FailMode:   *failMode,
		Logger:     logger,
		Metrics:    metrics,
		OnDecision: logDecision(logger),
	})(proxy)

	statusMux := http.NewServeMux()
	statusMux.Handle("/switchboard/status", httpadapter.StatusHandler(service))
	statusMux.Handle("/metrics", promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{}))

	mainMux := http.NewServeMux()
	if *statusListen == "" {
		mainMux.Handle("/switchboard/status", statusMux)
		mainMux.Handle("/metrics", statusMux)
	}
	mainMux.Handle("/", handler)

	mainServer := &http.Server{Addr: *listen, Handler: mainMux}
	servers := []*http.Server{mainServer}
	if *statusListen != "" {
		servers = append(servers, &http.Server{Addr: *statusListen, Handler: statusMux})
	}

	errCh := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *http.Server) {
			logger.Info("switchboard listening", zap.String("addr", server.Addr))
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}(server)
	}

	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-signalCtx.Done():
		logger.Info("shutting down")
	case err := <-errCh:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		_ = server.Shutdown(shutdownCtx)
	}
	return nil
}

func parseUpstream(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream %q: %w", raw, err)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid upstream %q: missing host", raw)
	}
	return parsed, nil
}

// logDecision surfaces each rule decision as structured access-log fields, the
// standalone equivalent of the Caddy handler exposing decision metadata.
func logDecision(logger *zap.Logger) func(*http.Request, engine.InvokeResult) {
	return func(r *http.Request, result engine.InvokeResult) {
		// Check first so the field slice is not built per request when debug
		// logging is off.
		if !logger.Core().Enabled(zap.DebugLevel) {
			return
		}
		fields := []zap.Field{
			zap.String("path", r.URL.Path),
			zap.String("decision", string(result.Action.Decision)),
			zap.String("bundle_id", result.BundleID),
		}
		if result.Action.Reason != "" {
			fields = append(fields, zap.String("reason", result.Action.Reason))
		}
		for key, value := range result.Action.Metadata {
			fields = append(fields, zap.String("metadata."+key, value))
		}
		logger.Debug("switchboard decision", fields...)
	}
}
