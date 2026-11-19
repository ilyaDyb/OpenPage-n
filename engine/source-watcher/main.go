package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	watcher "github.com/ratcker/OpenPage-n/engine/source-watcher/internal"
)

func main() {
	config, err := parseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	apiURL := os.Getenv("ENGINE_API_URL")
	if apiURL == "" {
		apiURL = "http://127.0.0.1:4444"
	}
	api, err := watcher.NewClient(apiURL, os.Getenv("ENGINE_WATCHER_TOKEN"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Info("starting", "interval", config.interval, "timeout", config.timeout, "once", config.once)
	if err := watcher.Run(ctx, api, config.interval, config.timeout, config.once, logger); err != nil {
		logger.Error("stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("stopped")
}
