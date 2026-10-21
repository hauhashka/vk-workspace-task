"""Фильтр --since: только issue, созданные не раньше начала указанной даты (UTC)."""

import pytest

import contract


def test_since_filters_by_created_at(settings, ghreport, api_pages):
    pages = api_pages(settings.repo)
    everything = contract.expected_issues(pages)
    if len(everything) < 2:
        pytest.skip("недостаточно issue для проверки фильтра")
    pivot = contract.parse_time(everything[len(everything) // 2]["created_at"])
    since = contract.utc_midnight(pivot)

    run = ghreport(settings.repo, "--since", since.strftime("%Y-%m-%d"), "--limit", "1000000")
    repo = run.report["repos"][0]

    assert repo["error"] is None
    assert all(contract.parse_time(i["created_at"]) >= since for i in repo["issues"])
    assert repo["issues"] == contract.expected_issues(pages, since=since)


def test_since_in_future_gives_empty_list(settings, ghreport):
    run = ghreport(settings.repo, "--since", "2999-01-01")
    repo = run.report["repos"][0]
    assert repo["error"] is None
    assert repo["issues"] == [] and repo["count"] == 0
