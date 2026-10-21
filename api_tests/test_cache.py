"""Кэширование: ETag, If-None-Match, 304 и неизменность отчёта."""

import contract


def test_second_run_uses_cache(settings, ghreport, tmp_path):
    cache_dir = tmp_path / "shared-cache"

    first = ghreport(settings.repo, cache_dir=cache_dir)
    second = ghreport(settings.repo, cache_dir=cache_dir)

    # Первый запуск: кэш пуст, условных запросов нет - 304 появиться не может.
    assert first.trace, "журнал первого запуска пуст"
    for rec in first.trace:
        assert rec["from_cache"] is False, rec
        assert rec["status"] != 304, f"304 на запрос без If-None-Match: {rec}"

    # Второй запуск: те же адреса, все ответы из кэша.
    assert [r["url"] for r in second.trace if r["status"] in (200, 304)] == \
        [r["url"] for r in first.trace if r["status"] == 200]
    pages = [r for r in second.trace if r["status"] in (200, 304)]
    assert pages and all(r["status"] == 304 and r["from_cache"] is True for r in pages), pages

    assert contract.without_generated_at(second.report) == contract.without_generated_at(first.report)


def test_fresh_cache_dir_is_not_shared(settings, ghreport, tmp_path):
    ghreport(settings.repo, "--limit", "1", cache_dir=tmp_path / "a")
    other = ghreport(settings.repo, "--limit", "1", cache_dir=tmp_path / "b")
    assert all(rec["from_cache"] is False for rec in other.trace)


def test_cache_does_not_contain_token(settings, ghreport, tmp_path):
    if not settings.token:
        import pytest
        pytest.skip("токен не задан")
    cache_dir = tmp_path / "cache"
    ghreport(settings.repo, "--limit", "1", cache_dir=cache_dir)
    files = [p for p in cache_dir.rglob("*") if p.is_file()]
    assert files, "кэш пуст после успешного запуска"
    for path in files:
        assert settings.token.encode() not in path.read_bytes(), path
