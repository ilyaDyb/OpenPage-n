package watcher

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const branch = "refs/heads/openpage"
const defaultGitTimeout = 30 * time.Second

// RemoteSHA читает SHA ветки опенпейч без клонирования репозитория.
func RemoteSHA(ctx context.Context, repository string) (string, error) {
	if err := validateURL(repository); err != nil {
		return "", err
	}

	// Для прямых вызовов тоже нужен тайм-аут; CLI задаёт свой срок через контекст.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultGitTimeout)
		defer cancel()
	}

	workDir, err := os.MkdirTemp("", "source-watcher-")
	if err != nil {
		return "", fmt.Errorf("create Git working directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	cmd := exec.CommandContext(ctx, "git", "-c", "credential.helper=", "-c", "http.followRedirects=false", "ls-remote", "--exit-code", "--refs", repository, branch)
	cmd.Dir = workDir
	cmd.Env = gitEnvironment()
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("git ls-remote timed out: %w", ctx.Err())
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return "", errors.New("branch refs/heads/openpage not found")
		}
		return "", fmt.Errorf("git ls-remote failed: %w", err)
	}
	sha, ref, found := strings.Cut(strings.TrimSuffix(string(output), "\n"), "\t")
	if !found || ref != branch || !validSHA(sha) {
		return "", errors.New("invalid git ls-remote response")
	}
	return sha, nil
}

func validateURL(repository string) error {
	u, err := url.Parse(repository)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Path == "" || u.Path == "/" || (u.Port() != "" && u.Port() != "443") {
		return errors.New("repository must be an HTTPS URL without credentials, query, fragment, or nonstandard port")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return errors.New("repository host must be a DNS name, not localhost or an IP address")
	}
	return nil
}

func validSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func gitEnvironment() []string {
	// Передаём Git только окружение, необходимое для HTTPS, без пользовательских настроек Git.
	var env []string
	for _, name := range []string{"PATH", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
}
