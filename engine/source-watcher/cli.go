package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	watcher "github.com/ratcker/OpenPage-n/engine/source-watcher/internal"
)

type cliConfig struct {
	projects []watcher.Project
	interval time.Duration
	timeout  time.Duration
	once     bool
}

func parseCLI(args []string) (cliConfig, error) {
	flags := flag.NewFlagSet("source-watcher", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("projects", "", "JSON file with projects to watch")
	interval := flags.Duration("interval", 30*time.Second, "time between checks")
	timeout := flags.Duration("timeout", 30*time.Second, "maximum time for each Git check")
	once := flags.Bool("once", false, "check every project once and exit")
	if err := flags.Parse(args); err != nil {
		return cliConfig{}, err
	}
	if *path == "" || *interval <= 0 || *timeout <= 0 || flags.NArg() != 0 {
		return cliConfig{}, fmt.Errorf("usage: source-watcher -projects projects.json [-interval 30s] [-timeout 30s] [-once]")
	}

	projects, err := loadProjects(*path)
	if err != nil {
		return cliConfig{}, fmt.Errorf("projects: %w", err)
	}
	if err := watcher.ValidateProjects(projects); err != nil {
		return cliConfig{}, fmt.Errorf("projects: %w", err)
	}
	return cliConfig{projects: projects, interval: *interval, timeout: *timeout, once: *once}, nil
}

func loadProjects(path string) ([]watcher.Project, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var projects []watcher.Project
	if err := decoder.Decode(&projects); err != nil {
		return nil, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("expected one JSON array")
		}
		return nil, err
	}
	return projects, nil
}
