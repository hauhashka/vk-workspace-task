"""Локальный сервер, совместимый с контрактом ghreport (эндпоинт списка issue).

Используется набором тестов, если GITHUB_API_BASE_URL не задан, и может
запускаться отдельно:

    python api_tests/stub_server.py --port 8080

Репозитории:
    stub/alpha       - несколько страниц, pull request-ы, повтор элемента на
                       границе страниц, одинаковые created_at;
    stub/beta        - одна страница, метки, пользователь null;
    stub/missing     - 404;
    stub/ratelimited - первый запрос получает 429 с retry-after, следующий
                       после паузы - 200; запрос раньше срока снова получает 429;
    stub/broken      - 200 с повреждённым JSON.
Токен, начинающийся с "invalid", получает 401 на любой запрос.

Переменная STUB_MUTATION включает намеренное нарушение контракта, чтобы
убедиться, что набор тестов его обнаруживает:
    missing_link      - у первой страницы нет заголовка Link;
    pr_without_flag   - у pull request-ов нет ключа pull_request;
    unconditional_304 - повторный запрос адреса без If-None-Match получает 304;
    broken_json       - вторая страница содержит повреждённый JSON;
    desc_order        - параметр direction игнорируется, выдача по убыванию.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import threading
import time
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlencode, urlsplit

RETRY_AFTER_SECONDS = 2
MUTATION = os.environ.get("STUB_MUTATION", "")


def _issue(repo: str, number: int, created: datetime, *, pr: bool = False,
           labels: list[str] | None = None, login: str | None = "octocat") -> dict:
    item = {
        "url": f"https://api.github.com/repos/{repo}/issues/{number}",
        "html_url": f"https://github.com/{repo}/{'pull' if pr else 'issues'}/{number}",
        "number": number,
        "state": "open",
        "title": f"Задача №{number} \"{repo}\"",
        "user": {"login": login, "id": number} if login is not None else None,
        "labels": [{"id": i, "name": name, "color": "ededed"} for i, name in enumerate(labels or [])],
        "created_at": created.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "updated_at": created.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "comments": 0,
    }
    if pr and MUTATION != "pr_without_flag":
        item["pull_request"] = {"url": f"https://api.github.com/repos/{repo}/pulls/{number}"}
    return item


def _alpha() -> list[dict]:
    start = datetime(2026, 1, 1, tzinfo=timezone.utc)
    items = []
    created = start
    for n in range(1, 261):
        if n % 10 != 0:  # каждый десятый создан одновременно с предыдущим
            created = start + timedelta(hours=7 * n)
        labels = ["bug"] if n % 3 == 0 else (["enhancement", "help wanted"] if n % 5 == 0 else [])
        items.append(_issue("stub/alpha", n, created, pr=(n % 7 == 0), labels=labels))
    # Номера внутри одного created_at идут не по порядку: сервер сортирует по created_at.
    items.sort(key=lambda i: (i["created_at"], -i["number"] if i["number"] % 20 == 0 else i["number"]))
    return items


def _beta() -> list[dict]:
    start = datetime(2025, 6, 1, tzinfo=timezone.utc)
    return [
        _issue("stub/beta", 11, start, labels=["bug", "Приоритет: высокий"]),
        _issue("stub/beta", 12, start + timedelta(days=1), login=None),
        _issue("stub/beta", 13, start + timedelta(days=2), pr=True),
        _issue("stub/beta", 14, start + timedelta(days=40), labels=["question"]),
    ]


class StubState:
    def __init__(self) -> None:
        self.repos = {"stub/alpha": _alpha(), "stub/beta": _beta()}
        self.lock = threading.Lock()
        self.blocked_until = 0.0
        self.last_was_limited = False
        self.seen_urls: set[str] = set()


class Handler(BaseHTTPRequestHandler):
    server_version = "ghreport-stub/1.0"
    state: StubState

    def log_message(self, fmt, *args):  # тишина в выводе pytest
        pass

    def _send(self, status: int, body: bytes = b"", headers: dict | None = None) -> None:
        self.send_response(status)
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.send_header("Content-Length", str(len(body)))
        if body:
            self.send_header("Content-Type", "application/json; charset=utf-8")
        self.end_headers()
        if body and self.command != "HEAD":
            self.wfile.write(body)

    def _json(self, status: int, payload, headers: dict | None = None) -> None:
        self._send(status, json.dumps(payload, ensure_ascii=False).encode(), headers)

    def do_GET(self) -> None:  # noqa: N802
        auth = self.headers.get("Authorization", "")
        if auth.startswith("Bearer invalid"):
            self._json(401, {"message": "Bad credentials"})
            return

        parts = urlsplit(self.path)
        segments = parts.path.strip("/").split("/")
        if len(segments) != 4 or segments[0] != "repos" or segments[3] != "issues":
            self._json(404, {"message": "Not Found"})
            return
        repo = f"{segments[1]}/{segments[2]}"
        query = {k: v[-1] for k, v in parse_qs(parts.query).items()}

        if repo == "stub/broken":
            self._send(200, b'[{"number": 1, "title": "oops"')
            return
        if repo == "stub/ratelimited":
            self._rate_limited(query)
            return
        items = self.state.repos.get(repo)
        if items is None:
            self._json(404, {"message": "Not Found"})
            return
        self._page(repo, items, query)

    def _rate_limited(self, query: dict) -> None:
        with self.state.lock:
            now = time.monotonic()
            if now < self.state.blocked_until or not self.state.last_was_limited:
                if not self.state.last_was_limited:
                    self.state.blocked_until = now + RETRY_AFTER_SECONDS
                self.state.last_was_limited = True
                wait = max(1, math.ceil(self.state.blocked_until - now))
                self._json(429, {"message": "API rate limit exceeded"}, {"Retry-After": str(wait)})
                return
            self.state.last_was_limited = False
        items = [_issue("stub/ratelimited", 1, datetime(2026, 2, 1, tzinfo=timezone.utc))]
        self._page("stub/ratelimited", items, query)

    def _page(self, repo: str, items: list[dict], query: dict) -> None:
        if query.get("state", "open") != "open" or query.get("sort", "created") != "created":
            self._json(422, {"message": "Validation Failed"})
            return
        if query.get("direction", "desc") == "desc" or MUTATION == "desc_order":  # по умолчанию GitHub отдаёт desc
            items = list(reversed(items))
        try:
            per_page = min(max(int(query.get("per_page", "30")), 1), 100)
            page = max(int(query.get("page", "1")), 1)
        except ValueError:
            self._json(422, {"message": "Validation Failed"})
            return

        last = max(1, math.ceil(len(items) / per_page))
        start = (page - 1) * per_page
        chunk = items[start:start + per_page]
        # Имитация сдвига выдачи: последний элемент предыдущей страницы
        # повторяется в начале следующей - клиент обязан убрать повтор.
        if page > 1 and start - 1 < len(items):
            chunk = [items[start - 1]] + chunk

        host = self.headers.get("Host", "localhost")
        base = f"http://{host}/repos/{repo}/issues"

        def link(p: int) -> str:
            q = dict(query, page=str(p))
            return f"<{base}?{urlencode(q)}>"

        links = []
        if page < last:
            links.append(f'{link(page + 1)}; rel="next"')
            links.append(f'{link(last)}; rel="last"')
        if page > 1:
            links.append(f'{link(1)}; rel="first"')
            links.append(f'{link(page - 1)}; rel="prev"')

        body = json.dumps(chunk, ensure_ascii=False).encode()
        etag = 'W/"' + hashlib.sha1(body).hexdigest() + '"'
        headers = {"ETag": etag, "X-RateLimit-Limit": "5000", "X-RateLimit-Remaining": "4999"}
        if links and not (MUTATION == "missing_link" and page == 1):
            headers["Link"] = ", ".join(links)
        if self.headers.get("If-None-Match") == etag:
            self._send(304, b"", headers)
            return
        with self.state.lock:
            repeated = self.path in self.state.seen_urls
            self.state.seen_urls.add(self.path)
        if MUTATION == "unconditional_304" and repeated:
            self._send(304, b"", headers)
            return
        if MUTATION == "broken_json" and page == 2:
            body = body[: len(body) // 2]
        self._send(200, body, headers)


class StubServer:
    def __init__(self, host: str = "127.0.0.1", port: int = 0) -> None:
        handler = type("BoundHandler", (Handler,), {"state": StubState()})
        self.httpd = ThreadingHTTPServer((host, port), handler)
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)

    @property
    def url(self) -> str:
        host, port = self.httpd.server_address[:2]
        return f"http://{host}:{port}"

    def start(self) -> "StubServer":
        self.thread.start()
        return self

    def stop(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    server = StubServer(args.host, args.port)
    print(f"stub listening on {server.url}", flush=True)
    server.httpd.serve_forever()


if __name__ == "__main__":
    main()
