// Package github - минимальный HTTP-клиент к эндпоинту списка issue GitHub REST API
// с условными запросами, повторными попытками и учётом лимитов частоты.
package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ghreport/internal/cache"
	"ghreport/internal/trace"
)

const (
	APIVersion     = "2026-03-10"
	UserAgent      = "ghreport/1.0 (open issues report; +https://git.miem.hse.ru)"
	RequestTimeout = 30 * time.Second

	maxRedirects = 5
	maxBodySize  = 64 << 20
)

// Коды ошибок репозитория в отчёте.
const (
	CodeNotFound        = "not_found"
	CodeRateLimited     = "rate_limited"
	CodeRequestFailed   = "request_failed"
	CodeInvalidResponse = "invalid_response"
)

// ErrUnauthorized - ответ 401: фатальная ошибка для всего запуска.
var ErrUnauthorized = errors.New("unauthorized")

// RepoError - ошибка обработки одного репозитория.
type RepoError struct {
	Code string
	Err  error
}

func (e *RepoError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *RepoError) Unwrap() error { return e.Err }

func repoErr(code, format string, args ...any) *RepoError {
	return &RepoError{Code: code, Err: fmt.Errorf(format, args...)}
}

// Client выполняет запросы последовательно; не предназначен для параллельного использования.
type Client struct {
	Token string
	HTTP  *http.Client
	Cache *cache.Cache  // nil - кэш отключён
	Trace *trace.Writer // nil - журнал отключён
	Log   func(format string, args ...any)
	Sleep func(time.Duration)
	Now   func() time.Time

	// TokenHost - хост, которому разрешено отправлять токен (хост GITHUB_API_BASE_URL).
	TokenHost string

	RetryDelays         []time.Duration // задержки для сетевых ошибок и 5xx
	MaxRateLimitRetries int
	MaxRateLimitWait    time.Duration // ожидание дольше этого не выполняется
	DefaultRateLimitGap time.Duration // пауза без retry-after и x-ratelimit-reset
}

// NewClient создаёт клиент с параметрами из задания.
func NewClient(token, tokenHost string) *Client {
	return &Client{
		Token:     token,
		TokenHost: tokenHost,
		HTTP: &http.Client{
			Timeout: RequestTimeout,
			// Перенаправления обрабатываются вручную, чтобы каждая попытка
			// попадала в журнал, а токен не уходил на чужой хост.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		Log:                 func(string, ...any) {},
		Sleep:               time.Sleep,
		Now:                 time.Now,
		RetryDelays:         []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second},
		MaxRateLimitRetries: 5,
		MaxRateLimitWait:    15 * time.Minute,
		DefaultRateLimitGap: 60 * time.Second,
	}
}

// Page - тело успешного ответа и адрес следующей страницы.
type Page struct {
	Body []byte
	Next string
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// Get загружает страницу по адресу с учётом кэша, повторов и лимитов.
// Возвращает *RepoError или ErrUnauthorized.
func (c *Client) Get(rawURL string) (*Page, error) {
	entry, err := c.Cache.Get(rawURL)
	if err != nil {
		c.Log("cache: %v (ignored)", err)
		entry = nil
	}
	etag := ""
	if entry != nil {
		etag = entry.ETag
	}

	transientRetries, rateRetries := 0, 0
	for attempt := 1; ; attempt++ {
		resp, err := c.roundTrip(rawURL, etag, attempt)
		if err != nil || resp.status >= 500 {
			if transientRetries < len(c.RetryDelays) {
				delay := c.RetryDelays[transientRetries]
				transientRetries++
				c.Log("%s: %s, retry in %s", rawURL, describe(resp, err), delay)
				c.Sleep(delay)
				continue
			}
			return nil, repoErr(CodeRequestFailed, "%s", describe(resp, err))
		}

		switch {
		case resp.status == http.StatusOK:
			if !json.Valid(resp.body) {
				return nil, repoErr(CodeInvalidResponse, "response body is not valid JSON")
			}
			link := resp.header.Get("Link")
			if tag := resp.header.Get("ETag"); tag != "" {
				err := c.Cache.Put(cache.Entry{URL: rawURL, ETag: tag, Link: link, Body: string(resp.body)})
				if err != nil {
					c.Log("cache: %v (ignored)", err)
				}
			}
			return &Page{Body: resp.body, Next: NextLink(link, rawURL)}, nil

		case resp.status == http.StatusNotModified:
			if entry == nil {
				return nil, repoErr(CodeInvalidResponse, "304 Not Modified without a conditional request")
			}
			link := resp.header.Get("Link")
			if link == "" {
				link = entry.Link
			}
			return &Page{Body: []byte(entry.Body), Next: NextLink(link, rawURL)}, nil

		case resp.status == http.StatusUnauthorized:
			return nil, ErrUnauthorized

		case resp.status == http.StatusNotFound:
			return nil, repoErr(CodeNotFound, "404 Not Found")

		case isRateLimited(resp):
			wait := c.rateLimitWait(resp.header)
			if rateRetries >= c.MaxRateLimitRetries || wait > c.MaxRateLimitWait {
				return nil, repoErr(CodeRateLimited, "rate limit exceeded (status %d, wait %s)", resp.status, wait)
			}
			rateRetries++
			c.Log("%s: rate limited (status %d), waiting %s", rawURL, resp.status, wait)
			c.Sleep(wait)

		default:
			return nil, repoErr(CodeRequestFailed, "unexpected status %d", resp.status)
		}
	}
}

// roundTrip выполняет одну попытку, проходя по перенаправлениям 301/302/307/308.
// Каждый HTTP-запрос записывается в журнал.
func (c *Client) roundTrip(rawURL, etag string, attempt int) (*response, error) {
	cur := rawURL
	for hop := 0; ; hop++ {
		req, err := http.NewRequest(http.MethodGet, cur, nil)
		if err != nil {
			c.Trace.Log(http.MethodGet, cur, 0, attempt, false, "invalid url")
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("X-GitHub-Api-Version", APIVersion)
		if c.Token != "" && strings.EqualFold(req.URL.Host, c.TokenHost) {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}

		httpResp, err := c.HTTP.Do(req)
		if err != nil {
			c.logTrace(cur, 0, attempt, false, classify(err))
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(httpResp.Body, maxBodySize))
		httpResp.Body.Close()
		if err != nil {
			c.logTrace(cur, httpResp.StatusCode, attempt, false, classify(err))
			return nil, err
		}
		status := httpResp.StatusCode
		c.logTrace(cur, status, attempt, status == http.StatusNotModified && etag != "", "")

		switch status {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			loc := httpResp.Header.Get("Location")
			if loc == "" || hop >= maxRedirects {
				return &response{status: status, header: httpResp.Header, body: body}, nil
			}
			cur = resolve(cur, loc)
			continue
		}
		return &response{status: status, header: httpResp.Header, body: body}, nil
	}
}

func (c *Client) logTrace(url string, status, attempt int, fromCache bool, errText string) {
	if err := c.Trace.Log(http.MethodGet, url, status, attempt, fromCache, errText); err != nil {
		c.Log("trace: %v", err)
	}
}

func isRateLimited(r *response) bool {
	if r.status != http.StatusForbidden && r.status != http.StatusTooManyRequests {
		return false
	}
	if r.header.Get("Retry-After") != "" || r.header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	return strings.Contains(strings.ToLower(string(r.body)), "rate limit")
}

// rateLimitWait: retry-after, иначе до x-ratelimit-reset, иначе DefaultRateLimitGap.
func (c *Client) rateLimitWait(h http.Header) time.Duration {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil {
			return nonNegative(t.Sub(c.Now()))
		}
	}
	if v := strings.TrimSpace(h.Get("X-RateLimit-Reset")); v != "" {
		if epoch, err := strconv.ParseInt(v, 10, 64); err == nil {
			// +1 с: время сброса округлено до секунды вниз.
			return nonNegative(time.Unix(epoch, 0).Sub(c.Now()) + time.Second)
		}
	}
	return c.DefaultRateLimitGap
}

func nonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func classify(err error) string {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "timeout"
	}
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Timeout() {
		return "timeout"
	}
	return "network"
}

func describe(r *response, err error) string {
	if err != nil {
		return classify(err) + ": " + err.Error()
	}
	return fmt.Sprintf("server error %d", r.status)
}
