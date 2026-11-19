package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	watcher "github.com/ratcker/OpenPage-n/engine/source-watcher/internal"
)

const testRepository = "https://example.com/project.git"
const testSHA = "0123456789abcdef0123456789abcdef01234567"

func fakeGit(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestInvalidRemoteSHAResponse(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
	}{
		{"empty response", ""},
		{"bad SHA", "not-a-sha\trefs/heads/openpage"},
		{"wrong branch", testSHA + "\trefs/heads/main"},
		{"extra line", testSHA + "\trefs/heads/openpage\n" + testSHA + "\trefs/heads/main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakeGit(t, "printf '%s\\n' '"+test.output+"'")
			_, err := watcher.RemoteSHA(context.Background(), testRepository)
			if err == nil || err.Error() != "invalid git ls-remote response" {
				t.Fatalf("got %v, want invalid response", err)
			}
		})
	}
}

func TestRemoteSHATimeout(t *testing.T) {
	fakeGit(t, "exec sleep 5")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := watcher.RemoteSHA(ctx, testRepository)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
}

func TestGitCommandOptions(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_ASKPASS", "/nonexistent/askpass")
	fakeGit(t, `
test "$1" = "-c" && test "$2" = "credential.helper=" &&
test "$3" = "-c" && test "$4" = "http.followRedirects=false" &&
test "$5" = "ls-remote" && test "$6" = "--exit-code" &&
test "$7" = "--refs" && test "$8" = "https://example.com/project.git" &&
test "$9" = "refs/heads/openpage" && test "$#" = "9" &&
test "$GIT_TERMINAL_PROMPT" = "0" && test "$GCM_INTERACTIVE" = "never" &&
test -z "$GIT_CONFIG_COUNT" && test -z "$GIT_ASKPASS" || exit 70
printf '%s\trefs/heads/openpage\n' `+testSHA)

	sha, err := watcher.RemoteSHA(context.Background(), testRepository)
	if err != nil || sha != testSHA {
		t.Fatalf("got SHA %q and error %v", sha, err)
	}
}

func TestInvalidInput(t *testing.T) {
	for _, repository := range []string{
		"http://example.com/project.git",
		"file:///tmp/project.git",
		"https://user:secret@example.com/project.git",
		"https://example.com:8443/project.git",
		"https://example.com/project.git?option=value",
		"https://example.com/project.git#fragment",
		"https://example.com/repo.git\n--upload-pack=bad",
		"https://localhost/project.git",
		"https://127.0.0.1/project.git",
		"https://example.com/",
	} {
		if _, err := watcher.RemoteSHA(context.Background(), repository); err == nil {
			t.Errorf("accepted invalid repository URL %q", repository)
		}
	}
}
