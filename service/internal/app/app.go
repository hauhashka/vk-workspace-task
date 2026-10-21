// Package app - точка входа ghreport: аргументы, окружение, коды возврата.
package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ghreport/internal/cache"
	"ghreport/internal/github"
	"ghreport/internal/report"
	"ghreport/internal/trace"
)

// Коды возврата.
const (
	ExitOK      = 0 // все репозитории обработаны
	ExitPartial = 1 // отчёт сформирован, но у части репозиториев есть error
	ExitUsage   = 2 // некорректные аргументы или окружение
	ExitFatal   = 3 // unauthorized или all_failed
)

const DefaultBaseURL = "https://api.github.com"

const usage = `usage: ghreport --repos <owner/repo[,owner/repo...]>
                [--limit <N>] [--since <YYYY-MM-DD>]
                [--out <file|->] [--trace <file>] [--cache-dir <dir>]

environment:
  GITHUB_TOKEN         токен GitHub (необязательно)
  GITHUB_API_BASE_URL  адрес API (по умолчанию https://api.github.com)
`

type repoName struct{ owner, name string }

type config struct {
	repos    []repoName
	limit    int
	since    *time.Time
	out      string
	trace    string
	cacheDir string
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func parseArgs(args []string) (*config, error) {
	fs := flag.NewFlagSet("ghreport", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repos := fs.String("repos", "", "")
	limit := fs.String("limit", "", "")
	since := fs.String("since", "", "")
	out := fs.String("out", "-", "")
	tracePath := fs.String("trace", "", "")
	cacheDir := fs.String("cache-dir", "", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, usageError{err.Error()}
	}
	if fs.NArg() > 0 {
		return nil, usageError{fmt.Sprintf("лишний аргумент %q", fs.Arg(0))}
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	cfg := &config{out: *out, trace: *tracePath, cacheDir: *cacheDir}
	if strings.TrimSpace(*repos) == "" {
		return nil, usageError{"--repos обязателен: owner/repo[,owner/repo...]"}
	}
	for _, r := range strings.Split(*repos, ",") {
		r = strings.TrimSpace(r)
		if !repoPattern.MatchString(r) {
			return nil, usageError{fmt.Sprintf("--repos: некорректное имя репозитория %q, ожидается owner/repo", r)}
		}
		owner, name, _ := strings.Cut(r, "/")
		cfg.repos = append(cfg.repos, repoName{owner, name})
	}
	if set["limit"] {
		n, err := strconv.Atoi(*limit)
		if err != nil || n < 1 {
			return nil, usageError{"--limit должен быть целым числом, не меньшим единицы"}
		}
		cfg.limit = n
	}
	if set["since"] {
		t, err := time.Parse("2006-01-02", *since)
		if err != nil {
			return nil, usageError{"--since должен быть датой в формате YYYY-MM-DD"}
		}
		cfg.since = &t
	}
	if cfg.out == "" {
		return nil, usageError{"--out не может быть пустым; для stdout используйте -"}
	}
	if set["trace"] && cfg.trace == "" {
		return nil, usageError{"--trace не может быть пустым"}
	}
	if set["cache-dir"] && cfg.cacheDir == "" {
		return nil, usageError{"--cache-dir не может быть пустым"}
	}
	return cfg, nil
}

// Run выполняет команду и возвращает код возврата.
func Run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	logf := func(format string, a ...any) { fmt.Fprintf(stderr, "ghreport: "+format+"\n", a...) }

	cfg, err := parseArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stderr, usage)
		return ExitOK
	}
	if err != nil {
		logf("%v", err)
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}

	base := strings.TrimRight(getenv("GITHUB_API_BASE_URL"), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	baseURL, err := url.Parse(base)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		logf("GITHUB_API_BASE_URL: некорректный адрес %q", base)
		return ExitUsage
	}

	client := github.NewClient(getenv("GITHUB_TOKEN"), baseURL.Host)
	client.Log = logf

	cacheDir := cfg.cacheDir
	if cacheDir == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			cacheDir = filepath.Join(dir, "ghreport")
		}
	}
	if cacheDir != "" {
		c, err := cache.Open(cacheDir)
		if err != nil {
			if cfg.cacheDir != "" {
				logf("%v", err)
				return ExitUsage
			}
			logf("%v; кэш отключён", err)
		} else {
			client.Cache = c
		}
	}

	if cfg.trace != "" {
		f, err := os.Create(cfg.trace)
		if err != nil {
			logf("--trace: %v", err)
			return ExitUsage
		}
		defer f.Close()
		client.Trace = trace.New(f, time.Now)
	}

	rep := report.Report{Repos: []report.Repo{}}
	failed := 0
	for _, r := range cfg.repos {
		full := r.owner + "/" + r.name
		issues, err := report.Collect(client, report.FirstPageURL(base, r.owner, r.name),
			report.Options{Limit: cfg.limit, Since: cfg.since})
		entry := report.Repo{Repo: full, Issues: []report.Issue{}}
		var repoErr *github.RepoError
		switch {
		case errors.Is(err, github.ErrUnauthorized):
			logf("%s: 401 Unauthorized - проверьте GITHUB_TOKEN", full)
			return fatal(stdout, "unauthorized")
		case errors.As(err, &repoErr):
			logf("%s: %v", full, err)
			code := repoErr.Code
			entry.Error = &code
			failed++
		case err != nil:
			logf("%s: %v", full, err)
			code := github.CodeRequestFailed
			entry.Error = &code
			failed++
		default:
			entry.Issues = issues
		}
		entry.Count = len(entry.Issues)
		rep.Repos = append(rep.Repos, entry)
	}

	if failed == len(cfg.repos) {
		return fatal(stdout, "all_failed")
	}

	rep.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := marshal(rep)
	if err != nil {
		logf("encode report: %v", err)
		return ExitFatal
	}
	if cfg.out == "-" {
		if _, err := stdout.Write(data); err != nil {
			logf("write report: %v", err)
			return ExitFatal
		}
	} else if err := os.WriteFile(cfg.out, data, 0o644); err != nil {
		logf("--out: %v", err)
		return ExitFatal
	}
	if failed > 0 {
		return ExitPartial
	}
	return ExitOK
}

func fatal(stdout io.Writer, code string) int {
	data, _ := marshal(map[string]string{"error": code})
	stdout.Write(data)
	return ExitFatal
}

func marshal(v any) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
