"""Общая инфраструктура: настройки из окружения, запуск ghreport, эталон API.

Переменные окружения (все необязательные, кроме оговорённых):
    GHREPORT_BIN                путь к бинарнику ghreport; если не задан,
                                собирается из ../service через `go build`;
    GITHUB_API_BASE_URL         адрес сервера; если не задан, поднимается
                                локальный stub_server.py;
    GITHUB_TOKEN                токен для сервера (передаётся службе);
    GHREPORT_TEST_REPO          репозиторий с несколькими страницами (stub/alpha);
    GHREPORT_TEST_MISSING_REPO  несуществующий репозиторий (stub/missing);
    GHREPORT_TEST_RATE_LIMITED_REPO
                                репозиторий, отвечающий 429 с retry-after
                                (stub/ratelimited); пустое значение - тест пропускается.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path

import pytest

import contract

HERE = Path(__file__).resolve().parent
STUB_TOKEN = "stub-token-0123456789-secret"
RUN_TIMEOUT = 150  # секунд на один запуск службы

_stub = None
_build_dir = None


def pytest_configure(config):
    global _stub, _build_dir
    if not os.environ.get("GITHUB_API_BASE_URL"):
        from stub_server import StubServer

        _stub = StubServer().start()
        os.environ["GITHUB_API_BASE_URL"] = _stub.url
        os.environ.setdefault("GITHUB_TOKEN", STUB_TOKEN)
    if not os.environ.get("GHREPORT_BIN"):
        if shutil.which("go") is None:
            raise pytest.UsageError("GHREPORT_BIN не задан, а go для сборки службы не найден")
        _build_dir = tempfile.mkdtemp(prefix="ghreport-bin-")
        binary = os.path.join(_build_dir, "ghreport")
        subprocess.run(["go", "build", "-o", binary, "."], cwd=HERE.parent / "service",
                       check=True, timeout=300)
        os.environ["GHREPORT_BIN"] = binary


def pytest_unconfigure(config):
    if _stub is not None:
        _stub.stop()
    if _build_dir is not None:
        shutil.rmtree(_build_dir, ignore_errors=True)


@dataclass(frozen=True)
class Settings:
    bin: str
    base: str
    token: str | None
    repo: str
    missing_repo: str
    rate_limited_repo: str
    is_stub: bool


@pytest.fixture(scope="session")
def settings() -> Settings:
    return Settings(
        bin=os.environ["GHREPORT_BIN"],
        base=os.environ["GITHUB_API_BASE_URL"].rstrip("/"),
        token=os.environ.get("GITHUB_TOKEN") or None,
        repo=os.environ.get("GHREPORT_TEST_REPO", "stub/alpha"),
        missing_repo=os.environ.get("GHREPORT_TEST_MISSING_REPO", "stub/missing"),
        rate_limited_repo=os.environ.get("GHREPORT_TEST_RATE_LIMITED_REPO", "stub/ratelimited"),
        is_stub=_stub is not None,
    )


@dataclass
class Run:
    proc: subprocess.CompletedProcess
    trace_path: Path
    elapsed: float
    _trace: list | None = field(default=None, repr=False)

    @property
    def report(self) -> dict:
        assert self.proc.returncode in (0, 1), f"rc={self.proc.returncode}\n{self.proc.stderr}"
        return json.loads(self.proc.stdout)

    @property
    def trace_text(self) -> str:
        return self.trace_path.read_text(encoding="utf-8") if self.trace_path.exists() else ""

    @property
    def trace(self) -> list[dict]:
        if self._trace is None:
            self._trace = [json.loads(line) for line in self.trace_text.splitlines() if line.strip()]
        return self._trace


_DEFAULT = object()


@pytest.fixture
def ghreport(settings, tmp_path):
    """Возвращает функцию запуска службы с собственным каталогом кэша теста."""
    counter = iter(range(1, 1000))

    def run(repos: str, *args: str, cache_dir: Path | None = None, token=_DEFAULT,
            timeout: float = RUN_TIMEOUT) -> Run:
        n = next(counter)
        trace_path = tmp_path / f"trace-{n}.jsonl"
        cmd = [settings.bin, "--repos", repos, "--out", "-",
               "--cache-dir", str(cache_dir or tmp_path / "cache"),
               "--trace", str(trace_path), *args]
        env = {k: v for k, v in os.environ.items() if k != "GITHUB_TOKEN"}
        env["GITHUB_API_BASE_URL"] = settings.base
        tok = settings.token if token is _DEFAULT else token
        if tok:
            env["GITHUB_TOKEN"] = tok
        started = time.monotonic()
        proc = subprocess.run(cmd, capture_output=True, text=True, env=env, timeout=timeout)
        return Run(proc, trace_path, time.monotonic() - started)

    return run


_pages_cache: dict[str, list] = {}


@pytest.fixture
def api_pages(settings):
    """Эталонные страницы репозитория, прочитанные напрямую из API."""

    def load(repo: str):
        if repo not in _pages_cache:
            _pages_cache[repo] = contract.fetch_pages(settings.base, repo, settings.token)
        return _pages_cache[repo]

    return load
