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

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/configuration"
	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
	"github.com/gcleroux/external-dns-dnscaster-webhook/pkg/webhook"
)

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
	mainRouter := chi.NewRouter()
	mainRouter.Get("/", p.Negotiate)
	mainRouter.Get("/records", p.Records)
	mainRouter.Post("/records", p.ApplyChanges)
	mainRouter.Post("/adjustendpoints", p.AdjustEndpoints)

	mainServer := createHTTPServer(fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort), mainRouter, config.ServerReadTimeout, config.ServerWriteTimeout)
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
