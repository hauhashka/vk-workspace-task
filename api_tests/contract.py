"""Независимое чтение API и вычисление ожидаемого отчёта по контракту.

Тесты не знают заранее состав выдачи сервера, поэтому эталон строится
прямым обходом того же эндпоинта (только GET, только GITHUB_API_BASE_URL).
"""

from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone

QUERY = "state=open&sort=created&direction=asc&per_page=100"
MAX_PAGES = 200


@dataclass
class Page:
    url: str
    items: list
    next_url: str | None


def first_url(base: str, repo: str) -> str:
    return f"{base.rstrip('/')}/repos/{repo}/issues?{QUERY}"


def parse_time(value: str) -> datetime:
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    parsed = datetime.fromisoformat(value)
    if parsed.tzinfo is None:
        raise ValueError(f"timestamp without zone: {value}")
    return parsed


def next_link(header: str | None) -> str | None:
    if not header:
        return None
    for part in header.split(","):
        section = part.split(";")
        if len(section) < 2:
            continue
        target = section[0].strip()
        if not (target.startswith("<") and target.endswith(">")):
            continue
        for param in section[1:]:
            key, _, value = param.strip().partition("=")
            if key.strip().lower() == "rel" and "next" in value.strip().strip('"').split():
                return target[1:-1]
    return None


def get_json(url: str, token: str | None, attempts: int = 5) -> tuple[list, str | None]:
    headers = {
        "Accept": "application/vnd.github+json",
        "User-Agent": "ghreport-api-tests",
        "X-GitHub-Api-Version": "2026-03-10",
    }
    if token:
        headers["Authorization"] = f"Bearer {token}"
    for attempt in range(attempts):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30) as resp:
                return json.loads(resp.read().decode("utf-8")), resp.headers.get("Link")
        except urllib.error.HTTPError as err:
            retry_after = err.headers.get("Retry-After")
            if err.code in (403, 429) and retry_after and attempt + 1 < attempts:
                time.sleep(min(int(retry_after), 60))
                continue
            raise
    raise RuntimeError("unreachable")


def fetch_pages(base: str, repo: str, token: str | None) -> list[Page]:
    """Обходит все страницы по Link rel="next", как требует контракт."""
    pages: list[Page] = []
    url: str | None = first_url(base, repo)
    while url:
        assert len(pages) < MAX_PAGES, "слишком много страниц или цикл в Link"
        items, link = get_json(url, token)
        assert isinstance(items, list), f"{url}: ответ не является массивом"
        nxt = next_link(link)
        pages.append(Page(url, items, nxt))
        url = nxt
    return pages


def to_report_issue(item: dict) -> dict:
    user = item.get("user") or {}
    labels = [label["name"] if isinstance(label, dict) else label for label in item.get("labels") or []]
    return {
        "number": item["number"],
        "title": item["title"],
        "author": user.get("login") or "",
        "labels": labels,
        "created_at": item["created_at"],
        "url": item["html_url"],
    }


def sort_key(issue: dict):
    return parse_time(issue["created_at"]), issue["number"]


def expected_issues(pages: list[Page], limit: int | None = None, since: datetime | None = None) -> list[dict]:
    """Эталон: фильтр PR и since, дедупликация, остановка на limit, сортировка."""
    seen: set[int] = set()
    result: list[dict] = []
    for page in pages:
        for item in page.items:
            if "pull_request" in item:
                continue
            if since is not None and parse_time(item["created_at"]) < since:
                continue
            if item["number"] in seen:
                continue
            seen.add(item["number"])
            result.append(to_report_issue(item))
            if limit is not None and len(result) >= limit:
                return sorted(result, key=sort_key)
    return sorted(result, key=sort_key)


def pull_request_numbers(pages: list[Page]) -> set[int]:
    return {item["number"] for page in pages for item in page.items if "pull_request" in item}


def utc_midnight(value: datetime) -> datetime:
    value = value.astimezone(timezone.utc)
    return datetime(value.year, value.month, value.day, tzinfo=timezone.utc)


def without_generated_at(report: dict) -> dict:
    return {k: v for k, v in report.items() if k != "generated_at"}
