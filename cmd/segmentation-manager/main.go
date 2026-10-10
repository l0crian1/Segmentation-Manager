package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"segmentation-manager/internal/app"
	"syscall"
	"time"
)

func main() {
	data := os.Getenv("DATA_DIR")
	if data == "" {
		data = "/data"
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8443"
	}
	a, err := app.New(data)
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer a.Close()
	cert, key, err := app.Certificate(data)
	if err != nil {
		slog.Error("TLS initialization failed", "error", err)
		os.Exit(1)
	}
	srv := &http.Server{Addr: addr, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 180 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	slog.Info("Segmentation Manager listening", "address", addr, "transport", "https")
	if err = srv.ListenAndServeTLS(cert, key); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
