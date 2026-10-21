package report

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"ghreport/internal/github"
)

type fakeFetcher struct {
	pages map[string]string // url -> body
	next  map[string]string // url -> next url
	got   []string
}

func (f *fakeFetcher) Get(url string) (*github.Page, error) {
	f.got = append(f.got, url)
	body, ok := f.pages[url]
	if !ok {
		return nil, fmt.Errorf("unexpected url %s", url)
	}
	return &github.Page{Body: []byte(body), Next: f.next[url]}, nil
}

func item(n int, created string, extra string) string {
	return fmt.Sprintf(`{"number":%d,"title":"t%d","user":{"login":"u%d"},"labels":[{"name":"bug"}],`+
		`"created_at":"%s","html_url":"https://github.com/o/r/issues/%d"%s}`, n, n, n, created, n, extra)
}

func numbers(issues []Issue) []int {
	var out []int
	for _, i := range issues {
		out = append(out, i.Number)
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func threePages() *fakeFetcher {
	return &fakeFetcher{
		pages: map[string]string{
			"p1": "[" + item(1, "2026-01-01T00:00:00Z", "") + "," +
				item(2, "2026-01-02T00:00:00Z", `,"pull_request":{"url":"x"}`) + "," +
				item(3, "2026-01-03T00:00:00Z", "") + "]",
			// №3 повторяется на границе страниц; №4 - pull request с null.
			"p2": "[" + item(3, "2026-01-03T00:00:00Z", "") + "," +
				item(4, "2026-01-04T00:00:00Z", `,"pull_request":null`) + "," +
				item(5, "2026-01-05T00:00:00Z", "") + "]",
			"p3": "[" + item(6, "2026-01-05T00:00:00Z", "") + "," + item(7, "2026-01-07T00:00:00Z", "") + "]",
		},
		next: map[string]string{"p1": "p2", "p2": "p3"},
	}
}

func TestCollectFiltersAndDedups(t *testing.T) {
	f := threePages()
	issues, err := Collect(f, "p1", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(issues); !equal(got, []int{1, 3, 5, 6, 7}) {
		t.Fatalf("got %v", got)
	}
	first := issues[0]
	if first.Author != "u1" || first.Title != "t1" || len(first.Labels) != 1 || first.Labels[0] != "bug" ||
		first.URL != "https://github.com/o/r/issues/1" || first.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("bad conversion: %+v", first)
	}
}

func TestCollectStopsAtLimit(t *testing.T) {
	f := threePages()
	issues, err := Collect(f, "p1", Options{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(issues); !equal(got, []int{1, 3, 5}) {
		t.Fatalf("got %v", got)
	}
	if len(f.got) != 2 {
		t.Fatalf("fetched %v, third page must not be requested", f.got)
	}
}

func TestCollectSince(t *testing.T) {
	since := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	issues, err := Collect(threePages(), "p1", Options{Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(issues); !equal(got, []int{5, 6, 7}) {
		t.Fatalf("got %v", got)
	}
}

func TestCollectSortsByCreatedThenNumber(t *testing.T) {
	f := &fakeFetcher{pages: map[string]string{
		"p": "[" + item(9, "2026-01-02T00:00:00Z", "") + "," + item(8, "2026-01-02T00:00:00Z", "") + "," +
			item(10, "2026-01-01T23:00:00-02:00", "") + "]",
	}}
	issues, err := Collect(f, "p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 2026-01-01T23:00-02:00 = 2026-01-02T01:00Z - позже остальных.
	if got := numbers(issues); !equal(got, []int{8, 9, 10}) {
		t.Fatalf("got %v", got)
	}
}

func TestCollectLabelsAndNullUser(t *testing.T) {
	f := &fakeFetcher{pages: map[string]string{
		"p": `[{"number":1,"title":"x","user":null,"labels":["a",{"name":"b"}],` +
			`"created_at":"2026-01-01T00:00:00Z","html_url":"u"}]`,
	}}
	issues, err := Collect(f, "p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if issues[0].Author != "" || len(issues[0].Labels) != 2 || issues[0].Labels[0] != "a" || issues[0].Labels[1] != "b" {
		t.Fatalf("got %+v", issues[0])
	}
}

func TestCollectInvalidResponses(t *testing.T) {
	for _, body := range []string{
		`{"message":"not a list"}`,
		`[{"number":1,"title":"x","created_at":"yesterday","html_url":"u"}]`,
		`[{"title":"x","created_at":"2026-01-01T00:00:00Z","html_url":"u"}]`,
	} {
		_, err := Collect(&fakeFetcher{pages: map[string]string{"p": body}}, "p", Options{})
		var re *github.RepoError
		if !errors.As(err, &re) || re.Code != github.CodeInvalidResponse {
			t.Errorf("body %s: want invalid_response, got %v", body, err)
		}
	}
}

func TestFirstPageURL(t *testing.T) {
	got := FirstPageURL("https://api.github.com", "cli", "cli")
	want := "https://api.github.com/repos/cli/cli/issues?state=open&sort=created&direction=asc&per_page=100"
	if got != want {
		t.Fatalf("got %s", got)
	}
}
