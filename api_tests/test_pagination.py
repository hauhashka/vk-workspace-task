"""Постраничная выдача, --limit, дедупликация и фильтрация pull request-ов."""

from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

import pytest

import contract


def matching_per_page(pages):
    """Сколько новых подходящих элементов даёт каждая страница."""
    seen, counts = set(), []
    for page in pages:
        n = 0
        for item in page.items:
            if "pull_request" not in item and item["number"] not in seen:
                seen.add(item["number"])
                n += 1
        counts.append(n)
    return counts


def test_limit_across_pages(settings, ghreport, api_pages):
    pages = api_pages(settings.repo)
    counts = matching_per_page(pages)
    if len(pages) < 2 or sum(counts) <= counts[0]:
        pytest.skip(f"{settings.repo}: выдача не многостраничная")
    limit = counts[0] + 1  # ровно один элемент со второй (или дальней) страницы

    run = ghreport(settings.repo, "--limit", str(limit))
    repo = run.report["repos"][0]

    assert repo["error"] is None
    assert repo["count"] == limit == len(repo["issues"])
    numbers = [i["number"] for i in repo["issues"]]
    assert len(numbers) == len(set(numbers)), "повторы по number"
    assert repo["issues"] == contract.expected_issues(pages, limit=limit)


def test_full_listing_matches_api(settings, ghreport, api_pages):
    """Без --limit в отчёт попадают все подходящие элементы всех страниц."""
    pages = api_pages(settings.repo)
    run = ghreport(settings.repo)
    repo = run.report["repos"][0]
    expected = contract.expected_issues(pages)
    missing = {i["number"] for i in expected} - {i["number"] for i in repo["issues"]}
    assert not missing, f"пропущены элементы: {sorted(missing)[:20]}"
    assert repo["issues"] == expected
    assert repo["count"] == len(expected)


def test_follows_link_header(settings, ghreport, api_pages):
    """Служба идёт строго по Link rel="next" и не запрашивает лишних страниц."""
    pages = api_pages(settings.repo)
    run = ghreport(settings.repo)
    assert run.proc.returncode == 0, run.proc.stderr
    urls = [rec["url"] for rec in run.trace if rec["status"] not in (301, 302, 307, 308)]
    # Повторные попытки дают несколько записей на один адрес - берём уникальные по порядку.
    distinct = list(dict.fromkeys(urls))
    assert distinct == [p.url for p in pages]


def test_pull_requests_are_excluded(settings, ghreport, api_pages):
    pages = api_pages(settings.repo)
    prs = contract.pull_request_numbers(pages)
    run = ghreport(settings.repo)
    issues = run.report["repos"][0]["issues"]
    reported = {i["number"] for i in issues}
    assert not (reported & prs), f"pull request-ы в отчёте: {sorted(reported & prs)[:20]}"
    assert all("/pull/" not in i["url"] for i in issues)
    if not prs:
        pytest.skip("в выдаче нет pull request-ов - проверено только отсутствие лишнего")


def test_server_listing_is_consistent(settings, api_pages):
    """Проверка самой выдачи, на которую опирается эталон: элементы идут
    по возрастанию created_at, а у последней страницы действительно нет продолжения."""
    pages = api_pages(settings.repo)
    stamps = [contract.parse_time(item["created_at"]) for page in pages for item in page.items]
    assert stamps == sorted(stamps), "выдача не отсортирована по возрастанию created_at"

    last = pages[-1]
    if len(last.items) >= 100:
        # Полная последняя страница подозрительна: запрашиваем следующую номером.
        parts = urlsplit(last.url)
        query = dict(parse_qsl(parts.query))
        query["page"] = str(int(query.get("page", len(pages))) + 1)
        probe = urlunsplit(parts._replace(query=urlencode(query)))
        items, _ = contract.get_json(probe, settings.token)
        assert items == [], 'есть следующая страница, но в заголовке Link нет rel="next"'
