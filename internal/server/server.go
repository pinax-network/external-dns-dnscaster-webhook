package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/configuration"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/metrics"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/webhook"
)

// responseSlack is how long past the request deadline the main server keeps
// the connection writable, so the handler can still send its 5xx.
const responseSlack = time.Second

func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("OK"))
	if err != nil {
		log.Error("healthcheck handler", "error writing response", err)
	}
}

func ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("OK"))
	if err != nil {
		log.Error("readiness handler", "error writing response", err)
	}
}

func Init(config configuration.Config, p *webhook.Webhook) (*http.Server, *http.Server) {
	mainRouter := newMainRouter(config, p)

	// The request deadline cancels the handler at ServerWriteTimeout. The
	// connection's write deadline gets a little slack on top so the 5xx can
	// still be written.
	writeTimeout := config.ServerWriteTimeout
	if writeTimeout > 0 {
		writeTimeout += responseSlack
	}

	mainServer := createHTTPServer(fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort), mainRouter, config.ServerReadTimeout, writeTimeout)
	go func() {
		log.Info("init server", "starting server on addr", mainServer.Addr)
		if err := mainServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("init server", "can't serve on addr", mainServer.Addr, "error", err)
		}
	}()

	healthRouter := chi.NewRouter()
	healthRouter.Get("/metrics", promhttp.Handler().ServeHTTP)
	healthRouter.Get("/healthz", HealthCheckHandler)
	healthRouter.Get("/readyz", ReadinessHandler)

	healthServer := createHTTPServer("0.0.0.0:8080", healthRouter, config.ServerReadTimeout, config.ServerWriteTimeout)
	go func() {
		log.Info("init server", "starting health server on addr", healthServer.Addr)
		if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("init server", "can't serve health on addr", healthServer.Addr, "error", err)
		}
	}()

	return mainServer, healthServer
}

func newMainRouter(config configuration.Config, p *webhook.Webhook) chi.Router {
	router := chi.NewRouter()
	router.Use(metrics.NewMetricsMiddleware(metrics.Get()).Handler)
	router.Use(requestDeadline(config.ServerWriteTimeout))
	router.Get("/", p.Negotiate)
	router.Get("/records", p.Records)
	router.Post("/records", p.ApplyChanges)
	router.Post("/adjustendpoints", p.AdjustEndpoints)
	return router
}

// requestDeadline cancels a request's context once timeout has passed, so a
// slow DNScaster call fails and the handler answers with a 5xx which external-dns
// will retry on the next reconcile loop
func requestDeadline(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if timeout <= 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func createHTTPServer(addr string, hand http.Handler, readTimeout, writeTimeout time.Duration) *http.Server {
	return &http.Server{
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		Addr:         addr,
		Handler:      hand,
	}
}

func ShutdownGracefully(mainServer *http.Server, healthServer *http.Server) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	sig := <-sigCh

	log.Info("shutdown", "shutting down servers due to received signal", sig)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := mainServer.Shutdown(ctx); err != nil {
		log.Error("shutdown", "error shutting down main server", err)
	}

	if err := healthServer.Shutdown(ctx); err != nil {
		log.Error("shutdown", "error shutting down health server", err)
	}
}
