"""Схема отчёта, типы полей, сортировка, соответствие полям API."""

from datetime import datetime, timezone

import contract

REPO_KEYS = {"repo", "error", "count", "issues"}
ISSUE_KEYS = {"number", "title", "author", "labels", "created_at", "url"}
ERRORS = {None, "not_found", "rate_limited", "request_failed", "invalid_response"}


def check_schema(report: dict) -> None:
    assert set(report) == {"generated_at", "repos"}
    assert isinstance(report["generated_at"], str)
    generated = contract.parse_time(report["generated_at"])
    assert generated.utcoffset().total_seconds() == 0, "generated_at не в UTC"
    assert abs((datetime.now(timezone.utc) - generated).total_seconds()) < 3600

    assert isinstance(report["repos"], list)
    for repo in report["repos"]:
        assert set(repo) == REPO_KEYS, repo.keys()
        assert isinstance(repo["repo"], str)
        assert repo["error"] in ERRORS
        assert type(repo["count"]) is int
        assert isinstance(repo["issues"], list)
        assert repo["count"] == len(repo["issues"])
        for issue in repo["issues"]:
            assert set(issue) == ISSUE_KEYS, issue.keys()
            assert type(issue["number"]) is int
            for key in ("title", "author", "created_at", "url"):
                assert isinstance(issue[key], str), key
            assert isinstance(issue["labels"], list)
            assert all(isinstance(label, str) for label in issue["labels"])
            contract.parse_time(issue["created_at"])
        keys = [contract.sort_key(i) for i in repo["issues"]]
        assert keys == sorted(keys), "issue не отсортированы по created_at, затем по number"
        numbers = [i["number"] for i in repo["issues"]]
        assert len(numbers) == len(set(numbers))


def test_report_schema(settings, ghreport):
    run = ghreport(settings.repo)
    report = run.report
    check_schema(report)
    assert report["repos"][0]["repo"] == settings.repo
    assert report["repos"][0]["error"] is None


def test_schema_with_error_repo(settings, ghreport):
    run = ghreport(f"{settings.missing_repo},{settings.repo}")
    report = run.report
    check_schema(report)
    assert [r["repo"] for r in report["repos"]] == [settings.missing_repo, settings.repo]


def test_fields_match_api(settings, ghreport, api_pages):
    pages = api_pages(settings.repo)
    by_number = {}
    for page in pages:
        for item in page.items:
            by_number.setdefault(item["number"], item)
    run = ghreport(settings.repo, "--limit", "50")
    for issue in run.report["repos"][0]["issues"]:
        assert issue == contract.to_report_issue(by_number[issue["number"]])
