# Сборка: docker build -t ghreport service/
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ghreport . \
    && mkdir -p /out/cache

# distroless/static: только CA-сертификаты и пользователь nonroot, без оболочки.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ghreport /usr/local/bin/ghreport
# Пустой каталог /cache, принадлежащий nonroot: именованный том, подключённый сюда,
# наследует владельца, и кэш можно сохранять между запусками (--cache-dir /cache).
COPY --from=build --chown=65532:65532 /out/cache /cache
# Рабочий каталог доступен nonroot на запись: здесь создаётся ./.ghreport-cache.
WORKDIR /home/nonroot
USER nonroot:nonroot
# Кэш по умолчанию - /home/nonroot/.ghreport-cache внутри контейнера; для сохранения
# между запусками подключите том и передайте --cache-dir.
ENTRYPOINT ["/usr/local/bin/ghreport"]
