package tests

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "source-watcher")
	build := exec.Command("go", "build", "-o", binary, "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Source Watcher: %v\n%s", err, out)
	}

	t.Run("single poll", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != listPath || r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Error("unexpected project list request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte("[]"))
		}))
		defer server.Close()
		t.Setenv("ENGINE_API_URL", server.URL)
		t.Setenv("ENGINE_WATCHER_TOKEN", testToken)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-interval", "45s", "-timeout", "5s", "-once")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "interval=45s timeout=5s once=true") {
			t.Fatalf("one-time check failed: %v\n%s", err, out)
		}
	})

	for _, test := range []struct {
		name, apiURL, token string
		args                []string
	}{
		{name: "missing token"},
		{name: "bad API URL", apiURL: "file:///tmp/api", token: testToken},
		{name: "zero interval", token: testToken, args: []string{"-interval", "0"}},
		{name: "removed JSON input", token: testToken, args: []string{"-projects", "projects.json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ENGINE_API_URL", test.apiURL)
			t.Setenv("ENGINE_WATCHER_TOKEN", test.token)
			out, err := exec.Command(binary, test.args...).CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("expected exit code 2, got %v: %s", err, out)
			}
		})
	}
}
