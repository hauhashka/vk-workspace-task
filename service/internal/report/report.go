// Package report собирает issue репозитория и формирует отчёт.
package report

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"time"

	"ghreport/internal/github"
)

type Issue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Author    string   `json:"author"`
	Labels    []string `json:"labels"`
	CreatedAt string   `json:"created_at"`
	URL       string   `json:"url"`

	created time.Time
}

type Repo struct {
	Repo   string  `json:"repo"`
	Error  *string `json:"error"`
	Count  int     `json:"count"`
	Issues []Issue `json:"issues"`
}

type Report struct {
	GeneratedAt string `json:"generated_at"`
	Repos       []Repo `json:"repos"`
}

// Fetcher - то, что нужно от HTTP-клиента (интерфейс упрощает тесты).
type Fetcher interface {
	Get(url string) (*github.Page, error)
}

type Options struct {
	Limit int        // 0 - без ограничения
	Since *time.Time // начало дня UTC; nil - без фильтра
}

// FirstPageURL - адрес первой страницы списка issue репозитория.
func FirstPageURL(base, owner, name string) string {
	return fmt.Sprintf("%s/repos/%s/%s/issues?state=open&sort=created&direction=asc&per_page=100",
		base, url.PathEscape(owner), url.PathEscape(name))
}

// rawIssue - поля ответа API, которые нужны отчёту.
type rawIssue struct {
	Number    *int                    `json:"number"`
	Title     *string                 `json:"title"`
	User      *struct{ Login string } `json:"user"`
	Labels    []json.RawMessage       `json:"labels"`
	CreatedAt *string                 `json:"created_at"`
	HTMLURL   *string                 `json:"html_url"`
	// Признак pull request - наличие ключа, даже со значением null.
	PullRequest json.RawMessage `json:"pull_request"`
}

// Collect обходит страницы через Link rel="next", фильтрует pull request-ы
// и issue раньше Since, устраняет повторы по number и останавливается на Limit.
// Ошибки - *github.RepoError или github.ErrUnauthorized.
func Collect(f Fetcher, firstURL string, opt Options) ([]Issue, error) {
	seen := make(map[int]bool)
	issues := []Issue{}
	for next := firstURL; next != ""; {
		page, err := f.Get(next)
		if err != nil {
			return nil, err
		}
		var raws []rawIssue
		if err := json.Unmarshal(page.Body, &raws); err != nil {
			return nil, &github.RepoError{Code: github.CodeInvalidResponse, Err: fmt.Errorf("decode issues: %w", err)}
		}
		for _, raw := range raws {
			if raw.PullRequest != nil {
				continue
			}
			issue, err := convert(raw)
			if err != nil {
				return nil, &github.RepoError{Code: github.CodeInvalidResponse, Err: err}
			}
			if opt.Since != nil && issue.created.Before(*opt.Since) {
				continue
			}
			if seen[issue.Number] {
				continue
			}
			seen[issue.Number] = true
			issues = append(issues, issue)
			if opt.Limit > 0 && len(issues) >= opt.Limit {
				return sorted(issues), nil
			}
		}
		next = page.Next
	}
	return sorted(issues), nil
}

func convert(raw rawIssue) (Issue, error) {
	if raw.Number == nil || raw.Title == nil || raw.CreatedAt == nil || raw.HTMLURL == nil {
		return Issue{}, fmt.Errorf("issue without number, title, created_at or html_url")
	}
	created, err := time.Parse(time.RFC3339, *raw.CreatedAt)
	if err != nil {
		return Issue{}, fmt.Errorf("issue #%d: bad created_at: %w", *raw.Number, err)
	}
	issue := Issue{
		Number:    *raw.Number,
		Title:     *raw.Title,
		Labels:    []string{},
		CreatedAt: *raw.CreatedAt,
		URL:       *raw.HTMLURL,
		created:   created,
	}
	if raw.User != nil {
		issue.Author = raw.User.Login
	}
	for _, l := range raw.Labels {
		// Метка - объект с полем name; документация допускает и строку.
		var obj struct{ Name string }
		var s string
		switch {
		case json.Unmarshal(l, &obj) == nil:
			issue.Labels = append(issue.Labels, obj.Name)
		case json.Unmarshal(l, &s) == nil:
			issue.Labels = append(issue.Labels, s)
		default:
			return Issue{}, fmt.Errorf("issue #%d: bad label %s", issue.Number, l)
		}
	}
	return issue, nil
}

func sorted(issues []Issue) []Issue {
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if !a.created.Equal(b.created) {
			return a.created.Before(b.created)
		}
		return a.Number < b.Number
	})
	return issues
}
