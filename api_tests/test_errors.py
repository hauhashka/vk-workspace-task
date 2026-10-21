"""Коды возврата, ошибки репозиториев, лимиты частоты, аргументы."""

import json

import pytest


def test_rate_limit_retry_after(settings, ghreport):
    if not settings.rate_limited_repo:
        pytest.skip("GHREPORT_TEST_RATE_LIMITED_REPO не задан")
    run = ghreport(settings.rate_limited_repo)

    assert run.proc.returncode == 0, run.proc.stderr
    repo = run.report["repos"][0]
    assert repo["error"] is None

    trace = run.trace
    limited = [i for i, r in enumerate(trace) if r["status"] == 429]
    assert limited, "сервер не ответил 429 - сценарий не воспроизведён"
    first = limited[0]
    # Пауза выдержана: повтор идёт по тому же адресу со следующим номером попытки,
    # и сервер больше не отвечает 429 (запрос раньше retry-after снова получил бы 429).
    retry = trace[first + 1]
    assert retry["url"] == trace[first]["url"]
    assert retry["attempt"] == trace[first]["attempt"] + 1
    assert retry["status"] == 200
    assert len(limited) == 1, f"повтор до истечения retry-after: {trace}"
    assert run.elapsed >= 1.0


def test_not_found_repo_gives_partial_report(settings, ghreport):
    run = ghreport(f"{settings.repo},{settings.missing_repo}")

    assert run.proc.returncode == 1, run.proc.stderr
    report = json.loads(run.proc.stdout)
    assert [r["repo"] for r in report["repos"]] == [settings.repo, settings.missing_repo]
    ok, missing = report["repos"]
    assert ok["error"] is None
    assert missing["error"] == "not_found"
    assert missing["count"] == 0 and missing["issues"] == []


def test_unauthorized_is_fatal(settings, ghreport):
    bad_token = "invalid-ghreport-test-token-7f3a9c"
    run = ghreport(settings.repo, token=bad_token)

    assert run.proc.returncode == 3, run.proc.stderr
    assert json.loads(run.proc.stdout) == {"error": "unauthorized"}
    for text in (run.proc.stdout, run.proc.stderr, run.trace_text):
        assert bad_token not in text


@pytest.mark.parametrize("args", [
    ["--limit", "0"],
    ["--limit", "abc"],
    ["--since", "21-10-2026"],
])
def test_bad_arguments(settings, ghreport, args):
    run = ghreport(settings.repo, *args)
    assert run.proc.returncode == 2
    assert run.proc.stdout == ""
    assert run.proc.stderr.strip()
    assert run.trace == [], "при неверных аргументах запросов быть не должно"


def test_bad_repo_name(settings, ghreport):
    run = ghreport("not-a-repo")
    assert run.proc.returncode == 2
    assert run.proc.stdout == ""
