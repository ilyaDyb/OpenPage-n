package watcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Run перед каждым обходом получает актуальное состояние проектов из Engine API.
func Run(ctx context.Context, api *Client, interval, gitTimeout time.Duration, once bool, logger *slog.Logger) error {
	if interval <= 0 {
		return errors.New("interval must be greater than zero")
	}
	if gitTimeout <= 0 {
		return errors.New("Git timeout must be greater than zero")
	}
	for ctx.Err() == nil {
		err := poll(ctx, api, gitTimeout, logger)
		if ctx.Err() != nil {
			return nil
		}
		if once {
			return err
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func poll(ctx context.Context, api *Client, gitTimeout time.Duration, logger *slog.Logger) error {
	projects, err := api.projects(ctx)
	if err != nil {
		if ctx.Err() == nil {
			logger.Error("could not list projects", "error", err)
		}
		return err
	}

	var firstErr error
	for _, p := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := checkProject(ctx, api, p, gitTimeout, logger); err != nil && ctx.Err() == nil {
			logger.Error("project check failed", "project_id", p.ID, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func checkProject(ctx context.Context, api *Client, p project, gitTimeout time.Duration, logger *slog.Logger) error {
	if p.ID == "" || p.ObservedRevision == nil || *p.ObservedRevision < 0 {
		return errors.New("invalid project state from Engine API")
	}
	var previousSHA string
	if p.ObservedSHA != nil {
		previousSHA = *p.ObservedSHA
		if !validSHA(previousSHA) {
			return errors.New("invalid observed SHA from Engine API")
		}
	}

	checkCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	sha, err := RemoteSHA(checkCtx, p.RepoURL)
	cancel()
	if err != nil {
		return fmt.Errorf("check Git HEAD: %w", err)
	}
	if sha == previousSHA {
		return nil
	}
	if err := api.observe(ctx, p, sha); err != nil {
		return fmt.Errorf("send observation: %w", err)
	}

	if p.ObservedSHA == nil {
		logger.Info("initial", "project_id", p.ID, "sha", sha)
	} else {
		logger.Info("changed", "project_id", p.ID, "previous_sha", previousSHA, "sha", sha, "detected_at", time.Now().Format(time.RFC3339))
	}
	return nil
}
