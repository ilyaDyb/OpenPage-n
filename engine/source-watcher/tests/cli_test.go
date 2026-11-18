package tests

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildWatcher(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "source-watcher")
	cmd := exec.Command("go", "build", "-o", binary, "..")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Source Watcher: %v\n%s", err, output)
	}
	return binary
}

func TestCLIOnceAndJSON(t *testing.T) {
	binary := buildWatcher(t)
	path := filepath.Join(t.TempDir(), "projects.json")
	data := `[
  {"id":"alpha","repo_url":"https://example.com/alpha.git"},
  {"id":"beta","repo_url":"https://example.com/beta.git","last_sha":"cccccccccccccccccccccccccccccccccccccccc"}
]`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	fakeGit(t, `case "$8" in
  *alpha.git) sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ;;
  *beta.git) sha=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb ;;
  *) exit 99 ;;
esac
printf '%s\trefs/heads/openpage\n' "$sha"`)

	cmd := exec.Command(binary, "-projects", path, "-interval", "45s", "-timeout", "5s", "-once")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("one-time check failed: %v\n%s", err, output)
	}
	logs := string(output)
	if !strings.Contains(logs, "msg=starting projects=2 interval=45s timeout=5s once=true") ||
		!strings.Contains(logs, "msg=initial project_id=alpha sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") ||
		!strings.Contains(logs, "msg=changed project_id=beta previous_sha=cccccccccccccccccccccccccccccccccccccccc sha=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") ||
		!strings.Contains(logs, "msg=stopped") {
		t.Fatalf("unexpected CLI output:\n%s", logs)
	}
}

func TestCLIRejectsInvalidInput(t *testing.T) {
	binary := buildWatcher(t)
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(validPath, []byte(`[{"id":"alpha","repo_url":"https://example.com/a.git"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	duplicatePath := filepath.Join(dir, "duplicate.json")
	if err := os.WriteFile(duplicatePath, []byte(`[{"id":"alpha"},{"id":"alpha"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	unknownPath := filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(unknownPath, []byte(`[{"id":"alpha","repo_ur1":"https://example.com/a.git"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	extraPath := filepath.Join(dir, "extra.json")
	if err := os.WriteFile(extraPath, []byte(`[] []`), 0600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		args []string
	}{
		{"missing projects", nil},
		{"zero interval", []string{"-projects", validPath, "-interval", "0"}},
		{"negative timeout", []string{"-projects", validPath, "-timeout", "-1s"}},
		{"extra argument", []string{"-projects", validPath, "extra"}},
		{"missing file", []string{"-projects", filepath.Join(dir, "missing.json")}},
		{"old repo flag", []string{"-repo", "https://example.com/a.git"}},
		{"old previous flag", []string{"-projects", validPath, "-previous", testSHA}},
		{"duplicate IDs", []string{"-projects", duplicatePath}},
		{"unknown JSON field", []string{"-projects", unknownPath}},
		{"extra JSON value", []string{"-projects", extraPath}},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := exec.Command(binary, test.args...).CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("expected exit code 2, got %v: %s", err, output)
			}
		})
	}
}
