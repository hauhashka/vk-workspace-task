# api_tests

Интеграционные тесты на pytest. Они проверяют `ghreport` как чёрный ящик:
запускают службу отдельным процессом и смотрят на отчёт, журнал `--trace`
и коды возврата.

## Запуск

```sh
python -m pip install -r api_tests/requirements.txt
pytest api_tests -q
```

Без переменных окружения набор сам собирает службу (`go build` в `service/`)
и поднимает локальный сервер `stub_server.py`, совместимый с контрактом.
Полный прогон (26 тестов) занимает около 3 секунд.

Против другого сервера:

```sh
GHREPORT_BIN=/path/to/ghreport GITHUB_API_BASE_URL=http://localhost:8080 pytest api_tests -q
```

## Переменные окружения

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `GHREPORT_BIN` | сборка из `../service` | путь к бинарнику службы |
| `GITHUB_API_BASE_URL` | локальный `stub_server.py` | адрес сервера; тесты не обращаются ни к чему другому |
| `GITHUB_TOKEN` | тестовый токен заглушки | токен, который передаётся службе и используется для эталонных запросов |
| `GHREPORT_TEST_REPO` | `stub/alpha` | репозиторий с многостраничной выдачей |
| `GHREPORT_TEST_MISSING_REPO` | `stub/missing` | репозиторий, отвечающий 404 |
| `GHREPORT_TEST_RATE_LIMITED_REPO` | `stub/ratelimited` | репозиторий, отвечающий 429 с `retry-after`; пустое значение пропускает тест |

Для проверки 401 служба запускается с заведомо неверным токеном
`invalid-…`. Такой токен отвергает и GitHub, и заглушка.

## Как устроен набор

* `conftest.py` - настройки из окружения и фикстура `ghreport(repos, *args, cache_dir=…, token=…)`.
  Она запускает службу с `--out - --trace <файл> --cache-dir <tmp>` и
  возвращает процесс, отчёт и журнал. У каждого теста свой временный каталог
  кэша (`tmp_path`). Таймаут одного запуска 150 с, произвольных задержек нет.
* `contract.py` - эталон. Состав выдачи заранее неизвестен, поэтому тесты сами
  обходят тот же эндпоинт по `Link rel="next"` (только GET и только
  `GITHUB_API_BASE_URL`), а затем вычисляют ожидаемый отчёт: фильтр PR и
  `since`, дедупликация, `limit`, сортировка, соответствие полей.
* `stub_server.py` - эталонный сервер для локального прогона. Репозитории:
  `stub/alpha` (3 страницы, PR, повтор на границе страниц, равные `created_at`
  с номерами не по порядку), `stub/beta`, `stub/missing` (404),
  `stub/ratelimited` (429 + `retry-after: 2`), `stub/broken` (битый JSON).
  Токен `invalid…` получает 401.

| Требование | Тесты |
|---|---|
| 1. Постраничная выдача и `--limit` | `test_pagination.py::test_limit_across_pages`, `test_full_listing_matches_api`, `test_default_limit_is_100`, `test_follows_link_header` |
| 2. Фильтрация pull request-ов | `test_pagination.py::test_pull_requests_are_excluded` |
| 3. `--since` | `test_since.py` |
| 4. Кэширование | `test_cache.py` |
| 5. 429 + `retry-after` | `test_errors.py::test_rate_limit_retry_after` |
| 6. 401 → код 3, 404 → код 1 и `error` | `test_errors.py::test_unauthorized_is_fatal`, `test_not_found_repo_gives_partial_report` |
| 7. Схема отчёта и сортировка | `test_schema.py` |
| 8. Журнал `--trace` | `test_trace.py` |

## Обнаружение нарушений контракта

Набор проверялся на намеренно сломанных сервере и службе. Нарушения на
стороне сервера включаются переменной `STUB_MUTATION`:

```sh
for m in missing_link pr_without_flag unconditional_304 broken_json desc_order; do
  STUB_MUTATION=$m pytest api_tests -q | tail -1
done
```

| Нарушение | Падает тестов |
|---|---|
| сервер: нет `Link` при наличии следующей страницы (`missing_link`) | 1 |
| сервер: PR без ключа `pull_request` (`pr_without_flag`) | 1 |
| сервер: 304 на запрос без `If-None-Match` (`unconditional_304`) | 15 |
| сервер: повреждённый JSON (`broken_json`) | 15 |
| сервер: неверное направление сортировки (`desc_order`) | 1 |
| служба: пропуск элемента при постраничной выдаче | 2 |
| служба: нет дедупликации по `number` | 6 |
| служба: нет фильтра pull request-ов | 5 |
| служба: `direction=desc` в запросе | 4 |
| служба: обратная сортировка отчёта | 6 |
| служба: нет паузы после 429 | 1 |
| служба: не отправляет `If-None-Match` | 1 |
| служба: неверная граница `--since` | 1 |
| служба: `--limit` на единицу больше | 2 |
| служба: не удаляет дубликаты в `--repos` | 1 |
| служба: `--limit` по умолчанию не 100 | 1 |
