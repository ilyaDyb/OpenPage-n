package tests

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

type cancelOnMessageWriter struct {
	bytes.Buffer
	message string
	cancel  context.CancelFunc
}

func (w *cancelOnMessageWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if bytes.Contains(p, []byte(w.message)) {
		w.cancel()
	}
	return n, err
}

func TestRunPreservesSHAAfterTimeout(t *testing.T) {
	newSHA := strings.Repeat("b", 40)
	fakeGit(t, "count_file=\"$0.count\"\n"+
		"count=0\n"+
		"if [ -f \"$count_file\" ]; then count=$(cat \"$count_file\"); fi\n"+
		"count=$((count + 1))\n"+
		"printf '%s' \"$count\" > \"$count_file\"\n"+
		"case \"$count\" in\n"+
		"1) printf '%s\\trefs/heads/openpage\\n' '"+testSHA+"' ;;\n"+
		"2) exec sleep 1 ;;\n"+
		"*) printf '%s\\trefs/heads/openpage\\n' '"+newSHA+"' ;;\n"+
		"esac")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output := &cancelOnMessageWriter{message: "msg=changed", cancel: cancel}
	logger := slog.New(slog.NewTextHandler(output, nil))
	projects := []watcher.Project{{ID: "single", RepoURL: testRepository}}
	if err := watcher.RunProjects(ctx, projects, time.Millisecond, 500*time.Millisecond, false, logger); err != nil {
		t.Fatal(err)
	}

	logs := output.String()
	if !strings.Contains(logs, "msg=initial project_id=single sha="+testSHA) ||
		!strings.Contains(logs, "msg=\"check failed\"") ||
		!strings.Contains(logs, "git ls-remote timed out") ||
		!strings.Contains(logs, "msg=changed project_id=single previous_sha="+testSHA+" sha="+newSHA) ||
		!strings.Contains(logs, "detected_at=") {
		t.Fatalf("missing expected events:\n%s", logs)
	}
	if strings.Count(logs, "msg=initial") != 1 || strings.Count(logs, "msg=changed") != 1 {
		t.Fatalf("unexpected duplicate events:\n%s", logs)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("run did not reach change before deadline: %v", ctx.Err())
	}
	if projects[0].LastSHA != newSHA {
		t.Fatalf("SHA was not updated: %+v", projects[0])
	}
}

func TestRunProjectsKeepsStatesSeparate(t *testing.T) {
	alphaInitial := testSHA
	alphaChanged := strings.Repeat("a", 40)
	betaInitial := strings.Repeat("b", 40)
	betaChanged := strings.Repeat("c", 40)
	fakeGit(t, "case \"$8\" in\n"+
		"https://example.com/alpha.git) name=alpha ;;\n"+
		"https://example.com/beta.git) name=beta ;;\n"+
		"*) exit 99 ;;\n"+
		"esac\n"+
		"count_file=\"$0.$name\"\n"+
		"count=0\n"+
		"if [ -f \"$count_file\" ]; then count=$(cat \"$count_file\"); fi\n"+
		"count=$((count + 1))\n"+
		"printf '%s' \"$count\" > \"$count_file\"\n"+
		"case \"$name:$count\" in\n"+
		"alpha:1) sha='"+alphaInitial+"' ;;\n"+
		"alpha:2) exit 128 ;;\n"+
		"alpha:*) sha='"+alphaChanged+"' ;;\n"+
		"beta:1) sha='"+betaInitial+"' ;;\n"+
		"beta:*) sha='"+betaChanged+"' ;;\n"+
		"esac\n"+
		"printf '%s\\trefs/heads/openpage\\n' \"$sha\"")

	projects := []watcher.Project{
		{ID: "alpha", RepoURL: "https://example.com/alpha.git"},
		{ID: "beta", RepoURL: "https://example.com/beta.git", LastSHA: betaInitial},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output := &cancelOnMessageWriter{
		message: "msg=changed project_id=alpha previous_sha=" + alphaInitial + " sha=" + alphaChanged,
		cancel:  cancel,
	}
	logger := slog.New(slog.NewTextHandler(output, nil))
	if err := watcher.RunProjects(ctx, projects, time.Millisecond, time.Second, false, logger); err != nil {
		t.Fatal(err)
	}

	logs := output.String()
	if ctx.Err() != context.Canceled {
		t.Fatalf("run did not reach the third pass: %v\n%s", ctx.Err(), logs)
	}
	if projects[0].LastSHA != alphaChanged || projects[1].LastSHA != betaChanged {
		t.Fatalf("project states were not updated independently: %+v", projects)
	}
	if !strings.Contains(logs, "msg=initial project_id=alpha sha="+alphaInitial) ||
		!strings.Contains(logs, "msg=\"check failed\" project_id=alpha") ||
		!strings.Contains(logs, "msg=changed project_id=beta previous_sha="+betaInitial+" sha="+betaChanged) ||
		!strings.Contains(logs, "msg=changed project_id=alpha previous_sha="+alphaInitial+" sha="+alphaChanged) ||
		strings.Contains(logs, "msg=initial project_id=beta") {
		t.Fatalf("unexpected events:\n%s", logs)
	}
	if strings.Index(logs, "msg=\"check failed\" project_id=alpha") >
		strings.Index(logs, "msg=changed project_id=beta") {
		t.Fatalf("beta was not checked after alpha failed:\n%s", logs)
	}
}

func TestRunProjectsContinuesAfterInvalidRepository(t *testing.T) {
	fakeGit(t, "printf '%s\\trefs/heads/openpage\\n' "+testSHA)
	projects := []watcher.Project{
		{ID: "bad", RepoURL: "http://example.com/repo.git"},
		{ID: "good", RepoURL: testRepository},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output := &cancelOnMessageWriter{message: "msg=initial project_id=good", cancel: cancel}
	logger := slog.New(slog.NewTextHandler(output, nil))
	if err := watcher.RunProjects(ctx, projects, time.Hour, time.Second, false, logger); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "msg=\"check failed\" project_id=bad") ||
		!strings.Contains(output.String(), "msg=initial project_id=good sha="+testSHA) ||
		projects[0].LastSHA != "" || projects[1].LastSHA != testSHA {
		t.Fatalf("unexpected result after invalid repository:\n%s\n%+v", output.String(), projects)
	}
}

func TestRunProjectsOnce(t *testing.T) {
	dir := fakeGit(t, "count_file=\"$0.count\"\n"+
		"count=0\n"+
		"if [ -f \"$count_file\" ]; then count=$(cat \"$count_file\"); fi\n"+
		"count=$((count + 1))\n"+
		"printf '%s' \"$count\" > \"$count_file\"\n"+
		"printf '%s\\trefs/heads/openpage\\n' '"+testSHA+"'")
	projects := []watcher.Project{
		{ID: "alpha", RepoURL: "https://example.com/alpha.git"},
		{ID: "beta", RepoURL: "https://example.com/beta.git", LastSHA: strings.Repeat("a", 40)},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	if err := watcher.RunProjects(ctx, projects, time.Hour, time.Second, true, logger); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatalf("one pass did not finish before the interval: %v", ctx.Err())
	}
	count, err := os.ReadFile(filepath.Join(dir, "git.count"))
	if err != nil {
		t.Fatal(err)
	}
	if string(count) != "2" || projects[0].LastSHA != testSHA || projects[1].LastSHA != testSHA {
		t.Fatalf("expected one check per project: count=%s projects=%+v", count, projects)
	}
	if !strings.Contains(output.String(), "msg=initial project_id=alpha sha="+testSHA) ||
		!strings.Contains(output.String(), "msg=changed project_id=beta previous_sha="+strings.Repeat("a", 40)+" sha="+testSHA) {
		t.Fatalf("unexpected events:\n%s", output.String())
	}
}

func TestValidateProjects(t *testing.T) {
	valid := watcher.Project{ID: "alpha", RepoURL: testRepository}
	for _, projects := range [][]watcher.Project{
		nil,
		{{RepoURL: testRepository}},
		{valid, valid},
	} {
		if err := watcher.ValidateProjects(projects); err == nil {
			t.Errorf("accepted invalid projects: %+v", projects)
		}
	}
}

func TestRunStopsWhileWaiting(t *testing.T) {
	fakeGit(t, "printf '%s\\trefs/heads/openpage\\n' "+testSHA)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &cancelOnMessageWriter{message: "msg=initial", cancel: cancel}
	logger := slog.New(slog.NewTextHandler(output, nil))

	start := time.Now()
	projects := []watcher.Project{{ID: "single", RepoURL: testRepository}}
	if err := watcher.RunProjects(ctx, projects, time.Hour, time.Second, false, logger); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not interrupt interval wait")
	}
	if !strings.Contains(output.String(), "msg=initial") {
		t.Fatal("first check did not run immediately")
	}
}

func TestRunDoesNotLogUnchanged(t *testing.T) {
	dir := fakeGit(t, "count_file=\"$0.count\"\n"+
		"count=0\n"+
		"if [ -f \"$count_file\" ]; then count=$(cat \"$count_file\"); fi\n"+
		"count=$((count + 1))\n"+
		"printf '%s' \"$count\" > \"$count_file\"\n"+
		"printf '%s\\trefs/heads/openpage\\n' '"+testSHA+"'")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	projects := []watcher.Project{{ID: "single", RepoURL: testRepository}}
	if err := watcher.RunProjects(ctx, projects, 10*time.Millisecond, time.Second, false, logger); err != nil {
		t.Fatal(err)
	}

	count, err := os.ReadFile(filepath.Join(dir, "git.count"))
	if err != nil {
		t.Fatal(err)
	}
	if string(count) == "1" {
		t.Fatal("repository was only checked once")
	}
	if strings.Count(output.String(), "msg=initial") != 1 || strings.Contains(output.String(), "unchanged") {
		t.Fatalf("unexpected log output:\n%s", output.String())
	}
}

func TestRunRejectsInvalidInterval(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	projects := []watcher.Project{{ID: "single", RepoURL: testRepository}}
	for _, interval := range []time.Duration{0, -time.Second} {
		if err := watcher.RunProjects(context.Background(), projects, interval, time.Second, false, logger); err == nil {
			t.Errorf("accepted interval %s", interval)
		}
	}
	if err := watcher.RunProjects(context.Background(), projects, time.Second, 0, false, logger); err == nil {
		t.Error("accepted zero Git timeout")
	}
}

func TestCheck(t *testing.T) {
	fakeGit(t, "printf '%s\\trefs/heads/openpage\\n' "+testSHA)

	for _, test := range []struct {
		name     string
		previous string
		want     watcher.Status
	}{
		{"first observation", "", watcher.Initial},
		{"same commit", testSHA, watcher.Unchanged},
		{"new commit", strings.Repeat("a", 40), watcher.Changed},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := watcher.Check(context.Background(), testRepository, test.previous)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || result.SHA != testSHA {
				t.Fatalf("got %+v, want status %s and SHA %s", result, test.want, testSHA)
			}
		})
	}
}

func TestRemoteSHAFailures(t *testing.T) {
	t.Run("missing branch", func(t *testing.T) {
		fakeGit(t, "exit 2")
		_, err := watcher.RemoteSHA(context.Background(), testRepository)
		if !errors.Is(err, watcher.ErrBranchNotFound) {
			t.Fatalf("got %v, want ErrBranchNotFound", err)
		}
	})

	t.Run("empty successful response", func(t *testing.T) {
		fakeGit(t, "exit 0")
		_, err := watcher.RemoteSHA(context.Background(), testRepository)
		if err == nil || err.Error() != "invalid git ls-remote response" {
			t.Fatalf("got %v, want invalid response", err)
		}
	})

	t.Run("unavailable repository", func(t *testing.T) {
		fakeGit(t, "exit 128")
		_, err := watcher.RemoteSHA(context.Background(), testRepository)
		if err == nil || !strings.Contains(err.Error(), "git ls-remote failed") {
			t.Fatalf("got %v, want Git error", err)
		}
	})

	for _, test := range []struct {
		name   string
		output string
	}{
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
	if _, err := watcher.Check(context.Background(), testRepository, "bad-sha"); err == nil {
		t.Error("accepted invalid previous SHA")
	}
}
