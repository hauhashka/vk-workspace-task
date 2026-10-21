# ghreport

Консольная служба на Go, которая строит JSON-сводку по открытым issue
репозиториев GitHub. Она работает с постраничной выдачей, условными запросами
(ETag), лимитами частоты и сбоями отдельных репозиториев. Внешних
зависимостей нет: HTTP-клиент и разбор ответа написаны на стандартной библиотеке.

## Запуск

```sh
cd service
go build -o ghreport .
GITHUB_TOKEN=... ./ghreport --repos cli/cli,golang/go --limit 20 --since 2026-01-01 \
    --out - --trace trace.jsonl --cache-dir .cache
```

```
ghreport --repos <owner/repo[,owner/repo...]>
         [--limit <N>] [--since <YYYY-MM-DD>]
         [--out <file|->] [--trace <file>] [--cache-dir <dir>]
```

| Аргумент | По умолчанию | Описание |
|---|---|---|
| `--repos` | обязателен | список `owner/repo` через запятую; дубликаты удаляются с сохранением первого вхождения, порядок сохраняется в отчёте |
| `--limit` | `100` | не больше N подходящих issue на репозиторий, целое ≥ 1 |
| `--since` | без фильтра | только issue с `created_at` ≥ начала дня (UTC) |
| `--out` | `-` | файл отчёта, `-` - stdout |
| `--trace` | нет | журнал HTTP-запросов (JSON Lines), файл перезаписывается |
| `--cache-dir` | `./.ghreport-cache` | каталог кэша ETag, создаётся автоматически |

Docker:

```sh
docker build -t ghreport service/
docker run --rm -e GITHUB_TOKEN=... ghreport --repos cli/cli --limit 20 --out -
docker run --rm -e GITHUB_TOKEN=... -e GITHUB_API_BASE_URL=http://host.docker.internal:8080 \
    ghreport --repos stub/alpha --out -
# кэш между запусками контейнера - через том:
docker run --rm -v ghreport-cache:/cache -e GITHUB_TOKEN=... ghreport --repos cli/cli --cache-dir /cache
```

Образ собирается в два этапа (`golang:1.22-alpine` → `distroless/static:nonroot`).
Рабочий каталог образа - `/home/nonroot`, поэтому кэш по умолчанию лежит в
`/home/nonroot/.ghreport-cache`.
Он запускается от пользователя `nonroot` (`USER`), не содержит ни секретов, ни
адреса API и не зависит от файлов на хосте. `.dockerignore` пропускает в
контекст сборки только исходники Go.

## Переменные окружения

| Переменная | Назначение |
|---|---|
| `GITHUB_TOKEN` | токен, отправляется как `Authorization: Bearer <token>`. Необязателен: без него действует лимит 60 запросов в час |
| `GITHUB_API_BASE_URL` | адрес API, по умолчанию `https://api.github.com`, завершающий слеш допускается. Если переменная задана, все запросы идут только на этот адрес, в том числе в контейнере |

Токен не встроен ни в код, ни в образ. Он не попадает в stdout, stderr,
журнал и кэш. Заголовок `Authorization` отправляется только на хост из
`GITHUB_API_BASE_URL`: при перенаправлении на другой хост токен не передаётся.

## Коды возврата и вывод

| Код | Когда | stdout |
|---|---|---|
| 0 | все репозитории обработаны | отчёт |
| 1 | отчёт сформирован, у части репозиториев есть `error` | отчёт |
| 2 | некорректные аргументы или `GITHUB_API_BASE_URL` | пусто, сообщение в stderr |
| 3 | ответ 401 (`{"error": "unauthorized"}`) или ошибка у всех репозиториев (`{"error": "all_failed"}`) | фатальный объект |

В stdout пишется только отчёт или фатальный объект. Диагностика идёт в stderr
с префиксом `ghreport: `. Если задан `--out <file>`, отчёт пишется в файл, а
фатальный объект всё равно выводится в stdout.

Пример отчёта (`count` равен длине `issues`, issue отсортированы по
`created_at`, затем по `number`):

```json
{
  "generated_at": "2026-10-21T16:51:30Z",
  "repos": [
    {
      "repo": "cli/cli",
      "error": null,
      "count": 1,
      "issues": [
        {"number": 1, "title": "Первый выпуск", "author": "octocat", "labels": ["bug"],
         "created_at": "2026-01-05T10:00:00Z", "url": "https://github.com/cli/cli/issues/1"}
      ]
    },
    {"repo": "example/missing", "error": "not_found", "count": 0, "issues": []}
  ]
}
```

Соответствие полей: `number` ← `number`, `title` ← `title`, `author` ← `user.login`
(пустая строка, если `user` равен null), `labels` ← `labels[].name`,
`created_at` ← `created_at` без изменений, `url` ← `html_url`.

Пример журнала:

```
{"ts":"2026-10-21T16:51:30Z","method":"GET","url":"https://api.github.com/repos/cli/cli/issues?state=open&sort=created&direction=asc&per_page=100","status":200,"attempt":1,"from_cache":false,"error":null}
{"ts":"2026-10-21T16:52:10Z","method":"GET","url":"https://api.github.com/repos/cli/cli/issues?state=open&sort=created&direction=asc&per_page=100","status":304,"attempt":1,"from_cache":true,"error":null}
```

## Архитектура

```
main.go                    os.Args/окружение → app.Run → os.Exit
internal/app               аргументы, окружение, цикл по репозиториям, коды возврата
internal/report            обход страниц, фильтрация, дедупликация, сортировка, схема отчёта
internal/github            HTTP-клиент: заголовки, перенаправления, кэш, повторы, лимиты, Link
internal/cache             файловый кэш ETag + тело
internal/trace             журнал JSON Lines
```

Для каждого репозитория:

1. Первый адрес: `GET {base}/repos/{owner}/{repo}/issues?state=open&sort=created&direction=asc&per_page=100`.
2. Заголовки: `Accept: application/vnd.github+json`,
   `User-Agent: ghreport/1.0 (open issues report; +https://git.miem.hse.ru)`,
   `X-GitHub-Api-Version: 2026-03-10`, `Authorization: Bearer …` (при наличии токена),
   `If-None-Match` (при наличии записи в кэше). Таймаут запроса 30 с.
3. Из страницы исключаются элементы с ключом `pull_request` (даже со значением
   null) и элементы с `created_at` раньше `--since`. Повторы по `number`
   отбрасываются. Как только набрано `--limit` элементов, обход прекращается.
4. Следующая страница берётся только из `Link: <…>; rel="next"`. Относительные
   адреса разрешаются относительно текущего. Адреса страниц вручную не строятся.
5. Итог сортируется по `created_at` (как время, с учётом зоны), затем по `number`.

Все запросы последовательные. Перенаправления 301/302/307/308 обрабатываются
вручную, до 5 переходов: каждый переход записывается в журнал отдельной
строкой, метод GET сохраняется, токен уходит только на исходный хост.

## Формат кэша

Каталог `--cache-dir` содержит по одному файлу на адрес, имя файла -
`sha256(url).json`:

```json
{"url": "https://api.github.com/repos/cli/cli/issues?...", "etag": "W/\"…\"",
 "link": "<…?page=2>; rel=\"next\"", "body": "[…тело ответа…]"}
```

* Запись создаётся или обновляется на ответ 200 с заголовком `ETag`.
  Пишется во временный файл, затем выполняется `rename`, поэтому прерванный
  запуск не оставит битую запись. Права 0600, каталог 0700.
* `link` хранится, чтобы продолжить обход по ответу 304, даже если сервер
  не повторил заголовок `Link`.
* Ответ 304 означает, что тело берётся из кэша, а в журнал пишется
  `from_cache: true`. Ответ 304 без нашего `If-None-Match` (записи в кэше
  нет) считается нарушением контракта и даёт `invalid_response`.
* Повреждённый файл кэша считается отсутствующим, в stderr выводится
  предупреждение. Токена в кэше нет: ключ записи - только адрес.

## Лимиты и повторы

* **Признак исчерпания лимита**: ответ 403 или 429, и при этом выполнено одно
  из условий: в теле есть «rate limit», есть заголовок `retry-after` или
  `x-ratelimit-remaining: 0`.
* **Пауза**: значение `retry-after` (секунды или HTTP-дата). Если его нет -
  ожидание до `x-ratelimit-reset` плюс 1 с. Если нет и его - 60 с.
  Повтор идёт по тому же адресу с номером попытки `attempt + 1`.
* **Ограничения**: не больше 5 повторов из-за лимита на один запрос, и одна
  пауза не длиннее 15 минут. Если нужно ждать дольше (например, до сброса
  часового лимита), репозиторий сразу получает `rate_limited`, и обработка
  переходит к следующему.
* **Сетевые ошибки и 5xx**: до трёх повторов с паузами 1, 2 и 4 с. После этого
  репозиторий получает `request_failed`. В журнале у сетевой ошибки
  `status: 0` и `error: "network"` или `"timeout"`.
* **Остальные ответы**: 404 даёт `not_found`. 200 с некорректным JSON или с
  неожиданной структурой даёт `invalid_response`. 401 завершает весь запуск
  с кодом 3. Прочие коды 4xx (например, 403 без признаков лимита) дают
  `request_failed` без повторов.

## Тесты и проверки

```sh
cd service
gofmt -l .        # пусто
go vet ./...      # без замечаний
go test ./...     # юнит-тесты, в т.ч. с -race
```

Юнит-тесты используют `httptest.Server` и подменённые `Sleep` и `Now`, поэтому
проходят мгновенно. Они покрывают заголовки, Link, кэш и 304, повторы с
задержками 1/2/4, `retry-after`, `x-ratelimit-reset`, вторичный лимит по
телу, перенаправления с токеном, все коды ошибок, разбор аргументов, `--out`
в файл, `unauthorized` и `all_failed`.
