# urlshortener

Сервис коротких ссылок на Go: слоистая архитектура (domain / usecase / adapters), Postgres, кэш в Redis, асинхронная проверка доступности целевых URL отдельным воркером, rate limiting через внешний gRPC-сервис, метрики Prometheus и трейсинг OpenTelemetry.

## Состав

- `internal/domain` — сущность `Link`, доменные ошибки, порты (`LinkRepository`, `CodeGenerator`, `LinkCache`, `HealthRepository`), типы для проверок доступности (`LinkHealth`, `DueCheck`, `HealthResult`).
- `pkg/base62` — кодирование/декодирование коротких кодов.
- `internal/usecase/shortener` — `Create`, `Get`, `Resolve`, `Deactivate`. Cache-aside через `domain.LinkCache`, планирование первой проверки доступности через `HealthScheduler`, учёт обращений к кэшу через `CacheMetricsRecorder`. Все три зависимости опциональны.
- `internal/usecase/checker` — `RunOnce`: забирает пачку просроченных проверок (`ClaimDue`), выполняет их в worker pool с ограниченной конкурентностью и записывает результат. После успеха следующая проверка планируется через фиксированный интервал, после неудачи — с экспоненциальным backoff и верхней границей. `httpprobe` — HTTP-проверка (GET с таймаутом; 5xx считается недоступностью).
- `internal/repository/memory`, `internal/repository/postgres`, `internal/repository/rediscache` — реализации портов: in-memory, Postgres (pgx v5), Redis (go-redis v9).
- `internal/auth` — API-ключи: `Principal`, `KeyStore`, `StaticKeyStore` (читает `API_KEYS=key:owner[:name]` из окружения), хелперы для контекста.
- `internal/ratelimit` — порт `Limiter` и gRPC-клиент `grpcclient` к отдельному сервису rate limiting (контракт — `api/ratelimit/v1/ratelimit.proto`).
- `internal/observability` — `NewRegistry()` (реестр Prometheus с Go/process-коллекторами) и `tracing.Setup()` (OpenTelemetry, экспорт по OTLP/HTTP).
- `internal/config` — конфигурация из переменных окружения.
- `internal/delivery/httpapi` — роутер на `net/http` (Go 1.22+), общая цепочка middleware (request id → logging → recover) и точечные обёртки на отдельных маршрутах (auth, rate limit, метрики). Пакет не импортирует pgx, grpc и prometheus — конкретные реализации собираются в `cmd/api`.
- `cmd/api` — сборка сервиса: config → трейсинг → метрики → Postgres → Redis → rate limiter → usecase → HTTP-сервер с graceful shutdown. `/metrics` отдаётся на основном порту, readiness проверяет все подключённые зависимости.
- `cmd/checker` — отдельный процесс: по тикеру (`CHECKER_INTERVAL_SECONDS`) вызывает `checker.Service.RunOnce`; поднимает небольшой HTTP-сервер только под `/metrics` и `/healthz` (`METRICS_ADDR`, по умолчанию `:9090`).
- `cmd/migrate` — накат миграций (`DATABASE_URL`).
- `test/integration` — интеграционные тесты на реальном Postgres через testcontainers-go (build tag `integration`, нужен Docker).
- `deployments/docker` — `Dockerfile.api` (собирает `api`, `migrate`, `checker`) и `docker-compose.yml`: postgres, redis, одноразовый `migrate`, api, checker, jaeger, prometheus, grafana с готовым дашбордом.

Опциональные зависимости включаются переменными окружения: `REDIS_ADDR` (кэш), `RATE_LIMITER_ADDR` (rate limiting), `OTEL_EXPORTER_OTLP_ENDPOINT` (трейсинг). Без них сервис работает, соответствующая функция просто выключена. Метрики включены всегда. Auth обязателен: `POST` и `DELETE /v1/links` требуют API-ключ.

## API

| Метод  | Путь                       | Auth                   | Описание |
|--------|----------------------------|------------------------|----------|
| POST   | `/v1/links`                | `X-API-Key`            | создать короткую ссылку; владелец берётся из ключа |
| GET    | `/v1/links/{code}`         | нет                    | метаданные ссылки (включая неактивные и просроченные) |
| GET    | `/v1/links/{code}/health`  | нет                    | статус доступности целевого URL |
| DELETE | `/v1/links/{code}`         | `X-API-Key`, владелец  | деактивировать |
| GET    | `/r/{code}`                | нет                    | редирект 302; 404, если ссылки нет, 410, если неактивна или просрочена |
| GET    | `/healthz`                 | нет                    | liveness |
| GET    | `/readyz`                  | нет                    | readiness: пингует БД, Redis и rate limiter (те, что включены) |
| GET    | `/metrics`                 | нет                    | метрики Prometheus |

Невалидный или отсутствующий ключ — `401`. `DELETE` дополнительно проверяет, что ключ принадлежит владельцу ссылки, иначе `403`.

## Rate limiting

`POST /v1/links` лимитируется по владельцу (из API-ключа), `GET /r/{code}` — по IP клиента. Сам алгоритм в этом сервисе не реализован: `internal/ratelimit.Limiter` — порт, за которым стоит gRPC-клиент к отдельному сервису rate limiting. Без `RATE_LIMITER_ADDR` лимитирование выключено. Ошибки лимитера не блокируют запрос (fail open).

Для сборки клиента нужна кодогенерация из proto:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# protoc: https://protobuf.dev/installation/

make proto   # генерирует internal/ratelimit/grpcclient/ratelimitv1
```

## Проверка доступности (checker)

1. `shortener.Service.Create` (если сконфигурирован `WithHealthScheduler`) создаёт строку в `link_health` со сроком проверки «сейчас». Это best-effort: ошибка планирования не мешает созданию ссылки.
2. `checker.Service.RunOnce` вызывает `HealthRepository.ClaimDue`: в короткой транзакции делает `SELECT ... FOR UPDATE SKIP LOCKED` по просроченным строкам, сдвигает их `next_check_at` вперёд на время аренды (`leaseFor`) и коммитит. Блокировка держится миллисекунды, а не всё время HTTP-проверки.
3. HTTP-запросы (`httpprobe.Prober`) выполняются вне транзакции, параллельно, не более `WithConcurrency` одновременно.
4. Результат каждой проверки записывается отдельным `UPDATE` (`Record`). Успех — следующая проверка через `WithUpInterval`; неудача — через `WithDownInterval * 2^(fails-1)`, но не больше `WithMaxDownInterval`.

Если реплика checker упала посреди проверки, арендованные строки снова станут доступны после истечения `leaseFor` — отдельная задача на очистку «зависших» проверок не нужна. Несколько реплик checker могут работать параллельно благодаря `SKIP LOCKED`.

Checker — отдельный бинарник, а не горутина в `cmd/api`. Проверки — это I/O против произвольных внешних серверов с непредсказуемой латентностью; внутри процесса API они конкурировали бы с обработкой запросов и усложняли graceful shutdown. Отдельный процесс масштабируется и деплоится независимо; без него сервис остаётся рабочим, просто у всех ссылок статус будет `unknown`.

Статус доступности не влияет на редирект: `GET /r/{code}` работает по таблице `links`, `link_health` — отдельная таблица. Ссылка, помеченная как `down`, продолжает редиректить; решение, доверять ли статусу, остаётся за клиентом.

## Наблюдаемость

### Метрики

`cmd/api` и `cmd/checker` отдают `/metrics` в формате Prometheus.

Prometheus-зависимые типы вынесены в отдельные подпакеты, а в основных пакетах остаются только небольшие интерфейсы:

| Интерфейс                        | Реализация                  |
|----------------------------------|-----------------------------|
| `middleware.HTTPMetricsRecorder` | `middleware/httpmetrics`    |
| `checker.MetricsRecorder`        | `checker/checkermetrics`    |
| `shortener.CacheMetricsRecorder` | `shortener/shortenermetrics`|

Благодаря этому `middleware`, `checker` и `shortener` тестируются без `client_golang` — через фейковые рекордеры.

Основные метрики:

- `http_requests_total{method,route,status}`, `http_request_duration_seconds{method,route}`. В `route` — паттерн маршрута (`GET /v1/links/{code}`), а не конкретный путь, иначе кардинальность росла бы вместе с числом ссылок. `/healthz`, `/readyz`, `/metrics` не размечены, чтобы probe- и scrape-трафик не засорял дашборды.
- `shortener_cache_lookups_total{outcome}` — `hit` / `negative_hit` / `miss` / `error`; по ней считается cache hit ratio.
- `checker_checks_total{result}`, `checker_check_duration_seconds`, `checker_claimed_batch_size`.
- Стандартные Go/process-коллекторы (`go_goroutines`, `process_resident_memory_bytes`, ...).

### Трейсинг

OpenTelemetry с экспортом по OTLP/HTTP, включается переменной `OTEL_EXPORTER_OTLP_ENDPOINT`. В docker-compose за ним стоит Jaeger (UI на `:16686`). HTTP-инструментация (`otelhttp`) оборачивает роутер в `cmd/api`, а не встроена в `httpapi`; в `cmd/checker` трейсинг — декоратор `tracingProber` вокруг `checker.Prober`.

### Grafana

`deployments/docker/grafana/dashboards/urlshortener.json` — дашборд из шести панелей: request rate по маршрутам, p50/p99 латентность, доля 5xx, cache hit ratio, результаты проверок checker, длительность проверок. Датасорс и дашборд подключаются через provisioning при `make docker-up` (`localhost:3000`, анонимный доступ для локального стенда).

## Запуск

```bash
make build            # go build ./...
make test             # go test ./... -race -cover
make vet
make fmt-check
make test-integration # интеграционные тесты, поднимают Postgres через testcontainers

# Весь стек через docker-compose (дев-ключ "dev-key"):
make docker-up        # api :8080, checker metrics :9090, jaeger :16686,
                      # prometheus :9091, grafana :3000
make docker-down      # остановить и удалить volume

curl -X POST localhost:8080/v1/links \
  -H 'X-API-Key: dev-key' -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}'
curl localhost:8080/v1/links/<code>/health   # после первого прохода checker
curl -i localhost:8080/r/<code>
```

Локальный запуск без контейнеров для api/checker:

```bash
docker compose -f deployments/docker/docker-compose.yml up -d postgres redis

export DATABASE_URL="postgres://urlshortener:urlshortener@localhost:5432/urlshortener?sslmode=disable"
export API_KEYS="dev-key:dev"                       # обязательно
export REDIS_ADDR="localhost:6379"                  # опционально
export RATE_LIMITER_ADDR="localhost:50051"          # опционально
export OTEL_EXPORTER_OTLP_ENDPOINT="localhost:4318" # опционально

go run ./cmd/migrate
make run           # cmd/api на :8080
make run-checker   # cmd/checker, /metrics на :9090
```

## Архитектурные решения

**Слои и зависимости**

- `domain` не знает о HTTP, SQL и Redis; `usecase` зависит только от интерфейсов; адаптеры реализуют эти интерфейсы, не меняя их.
- Интерфейсы объявляются на стороне потребителя и минимальны: `handler.Pinger` (`Ping(ctx) error`), `ratelimit.Limiter`, `shortener.HealthScheduler`, `*MetricsRecorder`. `*pgxpool.Pool`, `*postgres.HealthRepo` и т.д. подходят под них структурно, без адаптеров. В результате `httpapi` и usecase-пакеты компилируются и тестируются без внешних зависимостей, а конкретные реализации собираются в `cmd/*`.
- Роутер — стандартный `net/http` (Go 1.22+): паттернов вида `"POST /v1/links"` и `r.PathValue` достаточно для этого набора маршрутов, сторонний роутер не нужен.
- Порядок middleware: RequestID → Logging → Recover → mux. Recover ближе к mux, чтобы ловить паники хендлеров; Logging снаружи, чтобы залогировать итоговый статус 500 после восстановленной паники.
- Request ID — 8 байт из `crypto/rand` в hex; для корреляции логов этого достаточно, отдельный uuid-пакет не подключался.
- Время — зависимость: `Service` принимает `Clock` через опцию `WithClock`, тесты на TTL детерминированы.

**Хранение**

- Генерация кода — счётчик + base62, а не хэш от URL: уникальность без ретраев на коллизиях. В memory — атомарный счётчик, в Postgres — последовательность `link_codes`, что работает и с несколькими репликами API.
- Драйвер — pgx v5 с `pgxpool`. Миграции — goose через `database/sql` (`pgx/v5/stdlib`); рантайм и миграции используют разные подключения.
- Миграции встроены в бинарник через `go:embed`, а не читаются с диска.
- Ошибки Postgres переводятся в доменные в репозитории: `unique_violation` → `ErrLinkExists`, `pgx.ErrNoRows` и `RowsAffected() == 0` → `ErrLinkNotFound`. Usecase работает с одними и теми же sentinel-ошибками независимо от реализации.
- `Get` и `Resolve` различаются намеренно: публичный редирект использует `Resolve`, который скрывает неактивные и просроченные ссылки; хендлер метаданных использует `Get`, чтобы владелец видел, что ссылка деактивирована.

**Кэш**

- Кэш опционален и живёт за `domain.LinkCache`; без `WithCache` `Resolve` ходит напрямую в репозиторий.
- Ошибки Redis не роняют запрос: логируются и трактуются как промах. Redis здесь ускоритель, а не источник истины.
- Negative caching: промахи тоже кэшируются (`SetMissing` / `IsMissing`, TTL по умолчанию — минута), иначе запросы с несуществующими кодами полностью обходят кэш.
- Инвалидация при записи: `Create` и `Deactivate` вызывают `Invalidate` сразу, не дожидаясь TTL. Иначе деактивированная ссылка продолжала бы редиректить до истечения TTL, а ссылка, созданная под ранее «негативно» закэшированным кодом, какое-то время считалась бы несуществующей.
- TTL записи в кэше ограничен сроком жизни ссылки: `min(cacheTTL, ExpiresAt - now)`, где `now` берётся из инжектированного `Clock`.
- Позитивные и негативные записи хранятся под разными ключами; `Invalidate` удаляет оба одним `DEL`.
- Пакет называется `rediscache`, а не `redis`, чтобы не конфликтовать с именем импортируемого `github.com/redis/go-redis/v9`.

**Auth и rate limiting**

- `OwnerID` берётся из `Principal`, а не из тела запроса — иначе клиент мог бы создавать ссылки от чужого имени.
- `Deactivate` проверяет владельца, а не только валидность ключа: 401 и 403 здесь разные ситуации.
- Auth обязателен: пустой `StaticKeyStore{}` отклоняет все запросы, а не отключает проверку. Rate limiting и кэш — опциональны.
- Транспорт лимитера спрятан за портом `ratelimit.Limiter` (`Allow(ctx, key) (Decision, error)`); о gRPC знает только `internal/ratelimit/grpcclient`.
- Fail open: недоступность лимитера не превращается в отказ API. Компромисс осознанный: под атакой отказавший лимитер временно снимает защиту.
- Ключ лимита зависит от маршрута: `POST /v1/links` — по `OwnerID`, `GET /r/{code}` — по IP клиента. Middleware с `PrincipalKey` должен стоять после Auth, так как читает `Principal` из контекста.

**Checker**

- `ClaimDue` двухфазный: короткая транзакция на захват и сдвиг `next_check_at`, HTTP-запрос — уже вне транзакции. Держать соединение из пула открытым на время сетевого запроса к произвольному серверу — способ быстро исчерпать пул.
- Lease вместо отдельной очистки зависших проверок (см. раздел выше).
- Backoff считается в Go (`checker.Service.nextCheckAt`), а не в SQL через `CASE WHEN`: правило «сколько ждать после N неудач» покрыто юнит-тестами.
- `HealthGetter` в хендлере объявляет метод `GetByCode` с той же сигнатурой, что у `domain.HealthRepository`, поэтому `*postgres.HealthRepo` подходит без адаптера; `cmd/api` создаёт его один раз и передаёт и в `WithHealthScheduler`, и в роутер.

**Наблюдаемость**

- Метрики — за интерфейсом, реализация в отдельном подпакете (см. раздел «Метрики»).
- `route` в лейбле HTTP-метрик передаётся явно при регистрации маршрута (`deps.wrapMetrics("GET /v1/links/{code}", ...)`): `ServeMux` не отдаёт хендлеру сматченный паттерн, а копирование строки из `mux.Handle` исключает расхождение.
- `internal/observability` содержит только реестр и настройку трейсера; сами метрики лежат рядом с кодом, который их производит, чтобы `NewRegistry()` не тянул за собой лишнее.
- `otelhttp` оборачивает роутер в `cmd/api`, а не внутри `httpapi`: выбор трейсера и экспортёра — решение уровня сборки процесса, а не библиотеки.
- Grafana настраивается через provisioning (датасорс и дашборд из файлов, смонтированных read-only), чтобы после `make docker-up` панели были сразу.