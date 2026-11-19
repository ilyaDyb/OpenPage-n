package main

import (
	"flag"
	"fmt"
	"io"
	"time"
)

type cliConfig struct {
	interval time.Duration
	timeout  time.Duration
	once     bool
}

func parseCLI(args []string) (cliConfig, error) {
	flags := flag.NewFlagSet("source-watcher", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	interval := flags.Duration("interval", 30*time.Second, "time between checks")
	timeout := flags.Duration("timeout", 30*time.Second, "maximum time for each Git check")
	once := flags.Bool("once", false, "check every project once and exit")
	if err := flags.Parse(args); err != nil {
		return cliConfig{}, err
	}
	if *interval <= 0 || *timeout <= 0 || flags.NArg() != 0 {
		return cliConfig{}, fmt.Errorf("usage: source-watcher [-interval 30s] [-timeout 30s] [-once]")
	}
	return cliConfig{interval: *interval, timeout: *timeout, once: *once}, nil
}
