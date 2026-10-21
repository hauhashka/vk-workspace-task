"""Журнал --trace: поля записей, параметры запроса, отсутствие секретов."""

from urllib.parse import parse_qs, urlsplit

import contract

TRACE_KEYS = {"ts", "method", "url", "status", "attempt", "from_cache", "error"}


def test_trace_record_fields(settings, ghreport):
    run = ghreport(f"{settings.repo},{settings.missing_repo}")
    assert run.proc.returncode == 1, run.proc.stderr
    assert run.trace, "журнал пуст"
    for rec in run.trace:
        assert set(rec) == TRACE_KEYS, rec.keys()
        assert contract.parse_time(rec["ts"]).utcoffset().total_seconds() == 0
        assert rec["method"] == "GET"
        assert isinstance(rec["url"], str) and rec["url"].startswith(settings.base)
        assert type(rec["status"]) is int and (rec["status"] == 0 or 100 <= rec["status"] <= 599)
        assert type(rec["attempt"]) is int and rec["attempt"] >= 1
        assert isinstance(rec["from_cache"], bool)
        assert rec["error"] is None or isinstance(rec["error"], str)
        if rec["status"] == 0:
            assert rec["error"], "запись без ответа должна содержать error"
    assert any(rec["status"] == 404 for rec in run.trace)


def test_first_request_matches_contract(settings, ghreport):
    run = ghreport(settings.repo, "--limit", "1")
    first = run.trace[0]
    parts = urlsplit(first["url"])
    base_path = urlsplit(settings.base).path.rstrip("/")
    assert parts.path == f"{base_path}/repos/{settings.repo}/issues"
    query = {k: v[-1] for k, v in parse_qs(parts.query).items()}
    assert query == {"state": "open", "sort": "created", "direction": "asc", "per_page": "100"}
    assert first["attempt"] == 1


def test_trace_has_no_secrets(settings, ghreport):
    run = ghreport(settings.repo, "--limit", "1")
    text = run.trace_text
    assert text
    lowered = text.lower()
    assert "authorization" not in lowered
    assert "bearer" not in lowered
    if settings.token:
        for output in (text, run.proc.stdout, run.proc.stderr):
            assert settings.token not in output


def test_one_record_per_attempt_in_order(settings, ghreport):
    run = ghreport(settings.repo)
    stamps = [contract.parse_time(rec["ts"]) for rec in run.trace]
    assert stamps == sorted(stamps), "записи журнала не в порядке выполнения"
    for prev, cur in zip(run.trace, run.trace[1:]):
        if cur["url"] == prev["url"] and cur["attempt"] != 1:
            assert cur["attempt"] == prev["attempt"] + 1
