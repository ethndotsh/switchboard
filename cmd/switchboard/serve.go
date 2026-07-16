package main

import (
	"context"
	"encoding/json"
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
		"status-listen", "fail-mode", "poll-interval", "invoke-timeout", "cache-dir")
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", ":8080", "listen address")
	upstream := fs.String("upstream", "", "upstream URL or host:port to proxy to")
	registryURL := fs.String("registry", cfg.Registry, "registry URL, e.g. file://./registry")
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	channel := fs.String("channel", cfg.Channel, "channel name")
	statusListen := fs.String("status-listen", "", "separate listen address for /switchboard/status and /metrics (default: same listener)")
	failMode := fs.String("fail-mode", "open", "fail mode: open, closed, or last_good")
	pollInterval := fs.String("poll-interval", "", "registry poll interval (default 2s)")
	invokeTimeout := fs.String("invoke-timeout", "", "invocation timeout (default 50ms)")
	cacheDir := fs.String("cache-dir", "", "durable last-known-good cache directory")
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
		RegistryURL:   *registryURL,
		Namespace:     *namespace,
		Channel:       *channel,
		PollInterval:  *pollInterval,
		FailMode:      *failMode,
		InvokeTimeout: *invokeTimeout,
		CacheDir:      *cacheDir,
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
		FailMode: *failMode,
		Logger:   logger,
		Metrics:  metrics,
	})(proxy)

	statusMux := http.NewServeMux()
	statusMux.HandleFunc("/switchboard/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := statusJSON(service)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	})
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

func statusJSON(service *engine.Service) ([]byte, error) {
	return json.MarshalIndent(service.Status(), "", "  ")
}
