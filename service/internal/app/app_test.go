package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr, func(k string) string { return env[k] })
	return code, stdout.String(), stderr.String()
}

func TestBadArguments(t *testing.T) {
	cases := [][]string{
		{},
		{"--repos", ""},
		{"--repos", "noslash"},
		{"--repos", "a/b/c"},
		{"--repos", "a/b", "--limit", "0"},
		{"--repos", "a/b", "--limit", "x"},
		{"--repos", "a/b", "--since", "2026-13-01"},
		{"--repos", "a/b", "--out", ""},
		{"--repos", "a/b", "--unknown"},
		{"--repos", "a/b", "extra"},
	}
	for _, args := range cases {
		code, stdout, stderr := run(t, nil, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "ghreport: ") {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	_, _, stderr := run(t, nil, "--repos", "a/b", "--limit", "0")
	if !strings.Contains(stderr, "ghreport: --limit должен быть целым числом, не меньшим единицы") {
		t.Errorf("limit message: %q", stderr)
	}
}

func TestBadBaseURL(t *testing.T) {
	code, stdout, _ := run(t, map[string]string{"GITHUB_API_BASE_URL": "ftp://x"}, "--repos", "a/b")
	if code != ExitUsage || stdout != "" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

// stub - минимальный сервер: alpha - две страницы, missing - 404, broken - мусор.
func stub(t *testing.T, status401 bool) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status401 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		etag := fmt.Sprintf(`"%s?%s"`, r.URL.Path, r.URL.RawQuery)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		switch r.URL.Path {
		case "/repos/o/alpha/issues":
			w.Header().Set("ETag", etag)
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", `<`+srv.URL+`/repos/o/alpha/issues?page=2>; rel="next"`)
				w.Write([]byte(`[{"number":2,"title":"b","user":{"login":"x"},"labels":[],"created_at":"2026-01-02T00:00:00Z","html_url":"h2"},
					{"number":3,"title":"pr","user":{"login":"x"},"labels":[],"created_at":"2026-01-02T00:00:00Z","html_url":"h3","pull_request":{}}]`))
				return
			}
			w.Write([]byte(`[{"number":4,"title":"d","user":{"login":"y"},"labels":[{"name":"bug"}],"created_at":"2026-01-03T00:00:00Z","html_url":"h4"}]`))
		case "/repos/o/broken/issues":
			w.Write([]byte(`{not json`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad json %q: %v", s, err)
	}
	return v
}

func TestEndToEndWithCacheAndTrace(t *testing.T) {
	srv := stub(t, false)
	dir := t.TempDir()
	env := map[string]string{"GITHUB_API_BASE_URL": srv.URL, "GITHUB_TOKEN": "tok-SECRET"}
	tracePath := filepath.Join(dir, "trace.jsonl")
	args := []string{"--repos", "o/alpha,o/missing,o/broken", "--cache-dir", filepath.Join(dir, "cache"), "--trace", tracePath}

	code, out1, stderr := run(t, env, args...)
	if code != ExitPartial {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	rep := decode(t, out1)
	repos := rep["repos"].([]any)
	alpha := repos[0].(map[string]any)
	if alpha["error"] != nil || alpha["count"].(float64) != 2 {
		t.Fatalf("alpha: %v", alpha)
	}
	if repos[1].(map[string]any)["error"] != "not_found" || repos[2].(map[string]any)["error"] != "invalid_response" {
		t.Fatalf("repos: %v", repos)
	}

	code, out2, _ := run(t, env, args...)
	if code != ExitPartial {
		t.Fatalf("second run code=%d", code)
	}
	if fmt.Sprint(decode(t, out2)["repos"]) != fmt.Sprint(rep["repos"]) {
		t.Fatalf("cached report differs:\n%s\n%s", out1, out2)
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	trace := string(data)
	if strings.Count(trace, `"from_cache":true`) != 2 {
		t.Fatalf("trace of second run: %s", trace)
	}
	for _, s := range []string{out1, out2, stderr, trace} {
		if strings.Contains(s, "SECRET") {
			t.Fatal("token leaked")
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "cache"))
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, "cache", e.Name()))
		if strings.Contains(string(b), "SECRET") {
			t.Fatal("token in cache")
		}
	}
}

func TestOutToFile(t *testing.T) {
	srv := stub(t, false)
	out := filepath.Join(t.TempDir(), "report.json")
	code, stdout, _ := run(t, map[string]string{"GITHUB_API_BASE_URL": srv.URL},
		"--repos", "o/alpha", "--out", out, "--cache-dir", t.TempDir(), "--limit", "1")
	if code != ExitOK || stdout != "" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	data, _ := os.ReadFile(out)
	if decode(t, string(data))["repos"].([]any)[0].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("report: %s", data)
	}
}

func TestUnauthorizedIsFatal(t *testing.T) {
	srv := stub(t, true)
	code, stdout, _ := run(t, map[string]string{"GITHUB_API_BASE_URL": srv.URL}, "--repos", "o/alpha", "--cache-dir", t.TempDir())
	if code != ExitFatal || decode(t, stdout)["error"] != "unauthorized" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestAllFailed(t *testing.T) {
	srv := stub(t, false)
	code, stdout, _ := run(t, map[string]string{"GITHUB_API_BASE_URL": srv.URL}, "--repos", "o/missing,o/broken", "--cache-dir", t.TempDir())
	if code != ExitFatal || decode(t, stdout)["error"] != "all_failed" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestDefaultsAndDuplicateRepos(t *testing.T) {
	cfg, err := parseArgs([]string{"--repos", "o/a, o/b,o/a,o/b,o/c"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.limit != DefaultLimit || cfg.cacheDir != DefaultCacheDir || cfg.out != "-" {
		t.Fatalf("defaults: %+v", cfg)
	}
	var got []string
	for _, r := range cfg.repos {
		got = append(got, r.owner+"/"+r.name)
	}
	if strings.Join(got, ",") != "o/a,o/b,o/c" {
		t.Fatalf("repos: %v", got)
	}
}

func TestBaseURLTrailingSlash(t *testing.T) {
	srv := stub(t, false)
	code, stdout, stderr := run(t, map[string]string{"GITHUB_API_BASE_URL": srv.URL + "/"},
		"--repos", "o/alpha", "--cache-dir", t.TempDir())
	if code != ExitOK || decode(t, stdout)["repos"].([]any)[0].(map[string]any)["count"].(float64) != 2 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}
