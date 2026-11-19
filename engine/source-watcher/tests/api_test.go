package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	watcher "github.com/ratcker/OpenPage-n/engine/source-watcher/internal"
)

const (
	listPath  = "/internal/v1/watcher/projects"
	testToken = "watcher-test-token"
	projectID = "0812951a-c377-4045-8c09-83595bd5222d"
)

func testAPI(t *testing.T, handler http.HandlerFunc) *watcher.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			t.Error("wrong Authorization header")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	api, err := watcher.NewClient(server.URL, testToken)
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func testProject(id, repo, sha string, revision int) map[string]any {
	var observed any
	if sha != "" {
		observed = sha
	}
	return map[string]any{"id": id, "repo_url": repo, "observed_sha": observed, "observed_revision": revision}
}

func checkObservation(t *testing.T, r *http.Request, id, previous, sha string, revision int) {
	t.Helper()
	if r.URL.Path != listPath+"/"+id+"/observations" || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("unexpected observation route or content type: %s", r.URL.Path)
	}
	var prev any
	if previous != "" {
		prev = previous
	}
	want := map[string]any{"previous_sha": prev, "previous_revision": float64(revision), "sha": sha}
	var got map[string]any
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("observation = %v, want %v (decode error: %v)", got, want, err)
	}
}

func TestWatcherReportsChangesAndRefetchesAfterConflict(t *testing.T) {
	dir := fakeGit(t, `test "$9" = refs/heads/openpage || exit 99
printf '%s\trefs/heads/openpage\n' "$(cat "$0.sha")"`)
	a, b, c := testSHA, strings.Repeat("b", 40), strings.Repeat("c", 40)
	cycles := []struct {
		previous, head string
		revision, code int
	}{
		{"", a, 0, 200},
		{a, a, 1, 0},
		{a, b, 1, 200},
		{b, a, 2, 200},
		{a, c, 3, 409},
		{b, a, 4, 200},
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	next := 0
	postSeen := false
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "GET":
			if r.URL.Path != listPath {
				t.Errorf("unexpected GET path: %s", r.URL.Path)
			}
			if next > 0 && postSeen != (cycles[next-1].code != 0) {
				t.Errorf("unexpected observation count in cycle %d", next)
			}
			if next == len(cycles) {
				stop()
				return
			}
			step := cycles[next]
			next++
			postSeen = false
			if err := os.WriteFile(filepath.Join(dir, "git.sha"), []byte(step.head), 0600); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			_ = json.NewEncoder(w).Encode([]any{testProject(projectID, testRepository, step.previous, step.revision)})
		case "POST":
			if next == 0 || postSeen || cycles[next-1].code == 0 {
				t.Errorf("unexpected POST in cycle %d", next)
				w.WriteHeader(400)
				return
			}
			step := cycles[next-1]
			postSeen = true
			checkObservation(t, r, projectID, step.previous, step.head, step.revision)
			w.WriteHeader(step.code)
		}
	})
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if err := watcher.Run(ctx, api, time.Millisecond, time.Second, false, logger); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("polling did not complete: %v\n%s", ctx.Err(), logs.String())
	}
	out := logs.String()
	if strings.Count(out, "msg=initial") != 1 || strings.Count(out, "msg=changed") != 3 ||
		!strings.Contains(out, "HTTP 409") || strings.Contains(out, "sha="+c) {
		t.Fatalf("unexpected log after conflict:\n%s", out)
	}
}

func TestWatcherContinuesAfterProjectErrors(t *testing.T) {
	fakeGit(t, `case "$8" in
  */broken.git) exit 128 ;;
  */missing.git) exit 2 ;;
esac
printf '%s\trefs/heads/openpage\n' `+testSHA)
	var mu sync.Mutex
	var posted []string
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode([]any{
				testProject("broken", "https://example.com/broken.git", "", 0),
				testProject("missing", "https://example.com/missing.git", "", 0),
				testProject("bad-state", testRepository, "bad-sha", 1),
				testProject("rejected", testRepository, "", 0),
				testProject(projectID, testRepository, "", 0),
			})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		posted = append(posted, r.URL.Path)
		if strings.Contains(r.URL.Path, "/rejected/") {
			http.Error(w, testToken, http.StatusServiceUnavailable)
			return
		}
		checkObservation(t, r, projectID, "", testSHA, 0)
		w.WriteHeader(http.StatusOK)
	})
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if err := watcher.Run(context.Background(), api, time.Hour, time.Second, true, logger); err == nil {
		t.Fatal("one-time poll did not report a project failure")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{listPath + "/rejected/observations", listPath + "/" + projectID + "/observations"}
	if !reflect.DeepEqual(posted, want) {
		t.Errorf("observations = %v, want %v", posted, want)
	}
	out := logs.String()
	for _, text := range []string{"git ls-remote failed", "refs/heads/openpage not found", "invalid observed SHA", "HTTP 503", "msg=initial project_id=" + projectID} {
		if !strings.Contains(out, text) {
			t.Errorf("missing %q in log:\n%s", text, out)
		}
	}
	if strings.Contains(out, "msg=initial project_id=rejected") || strings.Contains(out, testToken) {
		t.Fatalf("failed delivery logged as successful or token leaked:\n%s", out)
	}
}

func TestWatcherRecoversWhenAPIBecomesAvailable(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	attempt := 0
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "GET" || r.URL.Path != listPath {
			t.Error("expected only project list requests")
		}
		attempt++
		switch attempt {
		case 1:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		case 2:
			w.WriteHeader(http.StatusUnauthorized)
		case 3:
			w.WriteHeader(http.StatusForbidden)
		case 4:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 5:
			_, _ = w.Write([]byte("[]"))
		default:
			stop()
		}
	})
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if err := watcher.Run(ctx, api, time.Millisecond, time.Second, false, logger); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if ctx.Err() != context.Canceled || strings.Count(out, "could not list projects") != 4 {
		t.Fatalf("polling did not recover: %v\n%s", ctx.Err(), out)
	}
	for _, text := range []string{"could not reach Engine API", "HTTP 401", "HTTP 403", "HTTP 503"} {
		if !strings.Contains(out, text) {
			t.Errorf("missing %q in log:\n%s", text, out)
		}
	}
}
