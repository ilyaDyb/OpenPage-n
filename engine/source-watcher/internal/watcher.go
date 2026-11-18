package watcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type Status string

const (
	Initial   Status = "initial"
	Unchanged Status = "unchanged"
	Changed   Status = "changed"
)

type Result struct {
	Status Status
	SHA    string
}

type Project struct {
	ID      string `json:"id"`
	RepoURL string `json:"repo_url"`
	LastSHA string `json:"last_sha"`
}

// Check сравнивает удалённый SHA с предыдущим. Пустой previousSHA означает первое наблюдение.
func Check(ctx context.Context, repository, previousSHA string) (Result, error) {
	if previousSHA != "" && !validSHA(previousSHA) {
		return Result{}, errors.New("previous SHA must be a 40- or 64-character lowercase hexadecimal string")
	}

	currentSHA, err := RemoteSHA(ctx, repository)
	if err != nil {
		return Result{}, err
	}

	result := Result{SHA: currentSHA}
	switch {
	case previousSHA == "":
		result.Status = Initial
	case previousSHA == currentSHA:
		result.Status = Unchanged
	default:
		result.Status = Changed
	}
	return result, nil
}

// RunProjects выполняет один проход при once или повторяет обход с заданным интервалом.
// Последний успешный SHA хранится в переданном срезе проектов.
func RunProjects(ctx context.Context, projects []Project, interval, gitTimeout time.Duration, once bool, logger *slog.Logger) error {
	if interval <= 0 {
		return errors.New("interval must be greater than zero")
	}
	if gitTimeout <= 0 {
		return errors.New("Git timeout must be greater than zero")
	}
	if err := ValidateProjects(projects); err != nil {
		return err
	}

	for {
		for i := range projects {
			if ctx.Err() != nil {
				return nil
			}
			checkProject(ctx, &projects[i], gitTimeout, logger)
		}
		if once {
			return nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func checkProject(ctx context.Context, project *Project, gitTimeout time.Duration, logger *slog.Logger) {
	checkCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	result, err := Check(checkCtx, project.RepoURL, project.LastSHA)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			logger.Error("check failed", "project_id", project.ID, "error", err)
		}
		return
	}

	switch result.Status {
	case Initial:
		logger.Info("initial", "project_id", project.ID, "sha", result.SHA)
	case Changed:
		logger.Info("changed", "project_id", project.ID, "previous_sha", project.LastSHA, "sha", result.SHA, "detected_at", time.Now().Format(time.RFC3339))
	}
	project.LastSHA = result.SHA
}

// ValidateProjects проверяет наличие проектов и уникальность их ID.
func ValidateProjects(projects []Project) error {
	if len(projects) == 0 {
		return errors.New("project list is empty")
	}

	seen := make(map[string]bool, len(projects))
	for _, project := range projects {
		if strings.TrimSpace(project.ID) == "" {
			return errors.New("project id is required")
		}
		if seen[project.ID] {
			return fmt.Errorf("duplicate project id %q", project.ID)
		}
		seen[project.ID] = true
	}
	return nil
}
