package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/ratcker/OpenPage-n/engine/engine-api/internal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("engine API stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	dsn := os.Getenv("ENGINE_DATABASE_URL")
	platformToken := os.Getenv("ENGINE_PLATFORM_TOKEN")
	watcherToken := os.Getenv("ENGINE_WATCHER_TOKEN")
	if dsn == "" || !validToken(platformToken) || !validToken(watcherToken) || platformToken == watcherToken {
		return errors.New("set ENGINE_DATABASE_URL and distinct ENGINE_PLATFORM_TOKEN and ENGINE_WATCHER_TOKEN")
	}
	addr := os.Getenv("ENGINE_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:4444"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return errors.New("invalid PostgreSQL configuration")
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		return errors.New("could not connect to PostgreSQL")
	}

	docsSetting := os.Getenv("ENGINE_DOCS_ENABLED")
	docsEnabled := docsSetting == "" || docsSetting == "true"
	server := &http.Server{
		Addr:              addr,
		Handler:           engineapi.NewHandler(db, platformToken, watcherToken, docsEnabled),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	done := make(chan error, 1)
	slog.Info("engine API starting", "addr", addr)
	go func() {
		done <- server.ListenAndServe()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func validToken(token string) bool {
	for _, char := range token {
		if char <= ' ' || char >= 127 {
			return false
		}
	}
	return token != ""
}
