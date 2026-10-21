package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ghreport/internal/cache"
	"ghreport/internal/trace"
)

type harness struct {
	client *Client
	trace  *bytes.Buffer
	sleeps []time.Duration
}

func newHarness(t *testing.T, srv *httptest.Server, token string) *harness {
	t.Helper()
	h := &harness{trace: &bytes.Buffer{}}
	u, _ := url.Parse(srv.URL)
	c := NewClient(token, u.Host)
	c.Sleep = func(d time.Duration) { h.sleeps = append(h.sleeps, d) }
	c.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	c.Trace = trace.New(h.trace, c.Now)
	cc, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.Cache = cc
	h.client = c
	return h
}

func (h *harness) records(t *testing.T) []trace.Record {
	t.Helper()
	var out []trace.Record
	for _, line := range strings.Split(strings.TrimSpace(h.trace.String()), "\n") {
		if line == "" {
			continue
		}
		var r trace.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad trace line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var re *RepoError
	if !errors.As(err, &re) || re.Code != code {
		t.Fatalf("want RepoError %s, got %v", code, err)
	}
}

func TestHeadersAndPagination(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != APIVersion {
			t.Errorf("X-GitHub-Api-Version = %q", got)
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "ghreport/") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Link", `<`+srv.URL+`/x?page=2>; rel="next", <`+srv.URL+`/x?page=3>; rel="last"`)
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	h := newHarness(t, srv, "secret")
	page, err := h.client.Get(srv.URL + "/x?page=1")
	if err != nil {
		t.Fatal(err)
	}
	if page.Next != srv.URL+"/x?page=2" {
		t.Fatalf("next = %q", page.Next)
	}
	if strings.Contains(h.trace.String(), "secret") {
		t.Fatal("token leaked into trace")
	}
}

func TestConditionalRequests(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if requests > 1 {
			t.Errorf("second request without If-None-Match")
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Link", `</p2>; rel="next"`)
		w.Write([]byte(`[{"number":1}]`))
	}))
	defer srv.Close()
	h := newHarness(t, srv, "")

	first, err := h.client.Get(srv.URL + "/p1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.client.Get(srv.URL + "/p1")
	if err != nil {
		t.Fatal(err)
	}
	if string(second.Body) != string(first.Body) {
		t.Fatalf("cached body %q != %q", second.Body, first.Body)
	}
	// 304 без Link: адрес следующей страницы восстанавливается из кэша.
	if second.Next != srv.URL+"/p2" {
		t.Fatalf("next from cache = %q", second.Next)
	}
	recs := h.records(t)
	if len(recs) != 2 || recs[0].FromCache || recs[0].Status != 200 || !recs[1].FromCache || recs[1].Status != 304 {
		t.Fatalf("unexpected trace: %+v", recs)
	}
}

func TestNotModifiedWithoutCacheIsInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	_, err := newHarness(t, srv, "").client.Get(srv.URL + "/p")
	wantCode(t, err, CodeInvalidResponse)
}

func TestServerErrorsAreRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	h := newHarness(t, srv, "")
	_, err := h.client.Get(srv.URL + "/p")
	wantCode(t, err, CodeRequestFailed)
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if len(h.sleeps) != 3 || h.sleeps[0] != want[0] || h.sleeps[1] != want[1] || h.sleeps[2] != want[2] {
		t.Fatalf("sleeps = %v", h.sleeps)
	}
	recs := h.records(t)
	for i, r := range recs {
		if r.Attempt != i+1 || r.Status != 502 {
			t.Fatalf("record %d: %+v", i, r)
		}
	}
}

func TestServerErrorThenSuccess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	if _, err := newHarness(t, srv, "").client.Get(srv.URL + "/p"); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	h := newHarness(t, srv, "")
	srv.Close()
	_, err := h.client.Get(addr + "/p")
	wantCode(t, err, CodeRequestFailed)
	recs := h.records(t)
	if len(recs) != 4 || recs[0].Status != 0 || recs[0].Error == nil || *recs[0].Error != "network" {
		t.Fatalf("trace: %+v", recs)
	}
}

func TestRateLimitRetryAfter(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	h := newHarness(t, srv, "")
	if _, err := h.client.Get(srv.URL + "/p"); err != nil {
		t.Fatal(err)
	}
	if len(h.sleeps) != 1 || h.sleeps[0] != 7*time.Second {
		t.Fatalf("sleeps = %v", h.sleeps)
	}
}

func TestRateLimitReset(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1700000030")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	h := newHarness(t, srv, "")
	if _, err := h.client.Get(srv.URL + "/p"); err != nil {
		t.Fatal(err)
	}
	if len(h.sleeps) != 1 || h.sleeps[0] != 31*time.Second {
		t.Fatalf("sleeps = %v", h.sleeps)
	}
}

func TestRateLimitBodyDefaultWaitAndGiveUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"You have exceeded a secondary rate limit"}`))
	}))
	defer srv.Close()
	h := newHarness(t, srv, "")
	_, err := h.client.Get(srv.URL + "/p")
	wantCode(t, err, CodeRateLimited)
	if len(h.sleeps) != h.client.MaxRateLimitRetries || h.sleeps[0] != 60*time.Second {
		t.Fatalf("sleeps = %v", h.sleeps)
	}
}

func TestForbiddenWithoutRateLimitFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Resource not accessible"}`))
	}))
	defer srv.Close()
	h := newHarness(t, srv, "")
	_, err := h.client.Get(srv.URL + "/p")
	wantCode(t, err, CodeRequestFailed)
	if len(h.sleeps) != 0 {
		t.Fatalf("unexpected sleeps %v", h.sleeps)
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		check  func(t *testing.T, err error)
	}{
		{404, `{"message":"Not Found"}`, func(t *testing.T, err error) { wantCode(t, err, CodeNotFound) }},
		{200, `[{"number":`, func(t *testing.T, err error) { wantCode(t, err, CodeInvalidResponse) }},
		{401, `{}`, func(t *testing.T, err error) {
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("want ErrUnauthorized, got %v", err)
			}
		}},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		_, err := newHarness(t, srv, "").client.Get(srv.URL + "/p")
		tc.check(t, err)
		srv.Close()
	}
}

func TestRedirectKeepsTokenOnSameHostOnly(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("token sent to a foreign host")
		}
		w.Write([]byte(`[]`))
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("token missing on same host")
		}
		switch r.URL.Path {
		case "/old":
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		case "/new":
			http.Redirect(w, r, other.URL+"/final", http.StatusTemporaryRedirect)
		}
	}))
	defer srv.Close()
	h := newHarness(t, srv, "secret")
	if _, err := h.client.Get(srv.URL + "/old"); err != nil {
		t.Fatal(err)
	}
	recs := h.records(t)
	if len(recs) != 3 || recs[0].Status != 301 || recs[1].Status != 307 || recs[2].Status != 200 {
		t.Fatalf("trace: %+v", recs)
	}
}
