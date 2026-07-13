package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"platform.local/commercialization/backend/internal/platform/config"
	"platform.local/commercialization/backend/internal/platform/health"
	"platform.local/commercialization/backend/internal/platform/httpx"
	"platform.local/commercialization/backend/internal/platform/requestid"
)

type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

type App struct {
	server          *http.Server
	logger          *slog.Logger
	shutdownTimeout time.Duration
	build           BuildInfo
}

func New(cfg config.Config, logger *slog.Logger, database health.Probe, build BuildInfo) *App {
	handler := NewHandler(logger, []health.Probe{database}, cfg.HealthCheckTimeout, build)
	return &App{
		server: &http.Server{
			Addr: cfg.HTTPAddress, Handler: handler,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
			MaxHeaderBytes: 1 << 20,
		},
		logger: logger, shutdownTimeout: cfg.ShutdownTimeout, build: build,
	}
}

func (a *App) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", a.server.Addr)
	if err != nil {
		return err
	}
	a.logger.Info("server started", "address", listener.Addr().String(), "version", a.build.Version, "commit", a.build.Commit)

	serveErr := make(chan error, 1)
	go func() { serveErr <- a.server.Serve(listener) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		a.logger.Info("server shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		_ = a.server.Close()
		return errors.Join(errors.New("graceful shutdown failed"), err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	a.logger.Info("server stopped")
	return nil
}

func NewHandler(logger *slog.Logger, probes []health.Probe, timeout time.Duration, build BuildInfo) http.Handler {
	probeHandler := health.New(probes, timeout, build.Version)
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", probeHandler.Live)
	mux.HandleFunc("/health/ready", probeHandler.Ready)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	})

	return requestid.Middleware(securityHeaders(accessLog(logger, recoverPanics(logger, mux))))
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic", "request_id", requestid.FromContext(r.Context()), "panic", recovered, "stack", string(debug.Stack()))
				if recorder, ok := w.(*responseRecorder); ok && recorder.status != 0 {
					return
				}
				httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.Info("http request", "request_id", requestid.FromContext(r.Context()), "method", r.Method, "path", r.URL.Path, "status", status, "duration_ms", time.Since(started).Milliseconds())
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
