# urlshortener

URL shortener на Go: domain-driven архитектура, кэширование,
асинхронная проверка доступности целевых ссылок. Полный план — в `docs/` (будет
добавлен по мере реализации следующих этапов).

## Статус: этап 7 — наблюдаемость (Prometheus/Grafana/трейсинг)

Реализовано:
- `internal/domain` — сущность `Link`, доменные ошибки, порты (`LinkRepository`,
  `CodeGenerator`, `LinkCache`, `HealthRepository`); типы `LinkHealth`,
  `DueCheck`, `HealthResult` для availability-чеков
- `pkg/base62` — кодирование/декодирование коротких кодов
- `internal/usecase/shortener` — бизнес-логика: `Create`, `Get`, `Resolve`,
  `Deactivate`, cache-aside через `domain.LinkCache` (опционально), плюс
  планирование первой проверки доступности через `HealthScheduler`
  (опционально); `Resolve` классифицирует каждый поход в кэш
  (`CacheMetricsRecorder` — опционально) для метрики hit ratio
- `internal/usecase/checker` — вторая бизнес-логика проекта: `RunOnce`
  забирает пачку просроченных проверок (`ClaimDue`), гоняет их через
  worker pool с ограниченной конкурентностью, пишет результат с фиксированным
  интервалом (успех) или экспоненциальным backoff с капом (неудача), при
  наличии `MetricsRecorder` репортит число проверок и их длительность.
  `httpprobe` — реальный HTTP-проубер (GET, таймаут, 5xx = down)
- `internal/repository/memory`, `internal/repository/postgres`,
  `internal/repository/rediscache` — реализации портов домена (in-memory,
  Postgres/pgx v5, Redis/go-redis v9)
- `internal/auth` — API-ключи: `Principal`, `KeyStore`, `StaticKeyStore`
  (`API_KEYS=key:owner[:name]` из env), контекст-хелперы
- `internal/ratelimit` — порт `Limiter` (не завязан на транспорт) +
  `grpcclient` — клиент к твоему существующему GCRA rate limiter'у по gRPC
  (контракт — `api/ratelimit/v1/ratelimit.proto`, см. раздел "Rate limiting"
  ниже — это единственное место, которое надо свести с реальным сервисом)
- **`internal/observability`** — `NewRegistry()` (общий Prometheus-реестр
  с Go/process-коллекторами) и `tracing.Setup()` (OpenTelemetry, экспорт
  трейсов по OTLP/HTTP). Сами метрики живут рядом с кодом, который их
  производит — `middleware.HTTPMetricsRecorder`, `checker.MetricsRecorder`,
  `shortener.CacheMetricsRecorder` — это **интерфейсы без единой внешней
  зависимости**; конкретные Prometheus-реализации (`httpmetrics`,
  `checkermetrics`, `shortenermetrics`) вынесены в отдельные подпакеты,
  чтобы `middleware`/`checker`/`shortener` остались тестируемыми без
  Prometheus. См. раздел "Наблюдаемость" ниже
- `internal/config` — конфиг из env-переменных, без внешних зависимостей.
  Кэш, rate limiting и трейсинг — опциональны (`REDIS_ADDR`/
  `RATE_LIMITER_ADDR`/`OTEL_EXPORTER_OTLP_ENDPOINT`), метрики — всегда
  включены, auth — обязателен: `POST`/`DELETE /v1/links` всегда за API-ключом
- `internal/delivery/httpapi` — роутер на `net/http` 1.22+, принимает единый
  `Deps` (сервис, ключи, лимитер, health-геттер, пингер, метрики — почти всё
  опционально кроме сервиса/ключей/логгера), общая цепочка middleware
  (request id → logging → recover) плюс точечные обёртки на конкретных
  маршрутах (auth, rate limit, метрики — не одинаково на всех, см. таблицу
  эндпоинтов). Пакет по-прежнему не импортирует ни `pgx`, ни `grpc`, ни
  теперь уже `prometheus` — все конкретные реализации собираются в `cmd/api`
- `cmd/api` — сборка сервиса: config → трейсинг (опц.) → метрики → Postgres →
  (опц.) Redis → (опц.) rate limiter → usecase → HTTP-сервер с graceful
  shutdown; readiness проверяет все подключённые зависимости; `/metrics` —
  на основном порту
- `cmd/checker` — **отдельный процесс**: тикер по `CHECKER_INTERVAL_SECONDS`
  → `checker.Service.RunOnce`; свой маленький HTTP-сервер только под
  `/metrics` и `/healthz` (`METRICS_ADDR`, по умолчанию `:9090`) — у чекера
  иначе вообще нет HTTP. Не часть `cmd/api` — см. раздел "Checker-сервис"
  ниже, почему
- `cmd/migrate` — отдельный бинарник для наката миграций (`DATABASE_URL` из env)
- `test/integration` — интеграционные тесты на реальном Postgres через
  **testcontainers-go** (build tag `integration`, нужен локальный Docker)
- `deployments/docker` — `Dockerfile.api` (собирает `api`+`migrate`+`checker`)
  + `docker-compose.yml`: postgres → redis → одноразовый `migrate` →
  `api` + `checker` + `jaeger` (трейсы) + `prometheus` + `grafana`
  (с готовым дашбордом из коробки)

Ещё не реализовано: очередь событий (изначально помечена опциональной для
этого пет-проекта и сознательно пропущена в пользу observability).

### Эндпоинты

| Метод | Путь | Auth | Что делает |
|---|---|---|---|
| `POST` | `/v1/links` | `X-API-Key` | создать короткую ссылку; `OwnerID` — из ключа |
| `GET` | `/v1/links/{code}` | нет | метаданные ссылки (видно и неактивные/просроченные) |
| `GET` | `/v1/links/{code}/health` | нет | статус доступности целевого URL |
| `DELETE` | `/v1/links/{code}` | `X-API-Key`, владелец | деактивировать |
| `GET` | `/r/{code}` | нет | редирект 302 (404/410, если ссылки нет/неактивна/просрочена) |
| `GET` | `/healthz` | нет | liveness |
| `GET` | `/readyz` | нет | readiness (пингует БД, Redis и rate limiter — что включено) |
| `GET` | `/metrics` | нет | Prometheus-метрики (в проде обычно закрыт сетевой политикой, не auth) |

`X-API-Key` берётся из `API_KEYS` (см. ниже). `DELETE` дополнительно проверяет,
что ключ принадлежит владельцу ссылки — иначе `403`, не только `401`.

## Rate limiting

`POST /v1/links` (по владельцу) и `GET /r/{code}` (по IP) лимит через
`internal/ratelimit.Limiter` — но не встроенным алгоритмом, а gRPC-клиентом к
уже существующему отдельному rate limiter'у.

```bash
# один раз:
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# и сам protoc: https://protobuf.dev/installation/

make proto   # генерирует internal/ratelimit/grpcclient/ratelimitv1
go get google.golang.org/grpc@v1.80.0
go mod tidy
```

Без `RATE_LIMITER_ADDR` rate limiting просто выключен — не ошибка, а
осознанный дефолт, тот же принцип, что и с `REDIS_ADDR`.

## Checker-сервис

`cmd/checker` — отдельный бинарник и отдельный процесс, не горутина внутри
`cmd/api`. Причина архитектурная, не формальная: проверка доступности — это
I/O-bound работа против произвольных внешних серверов с непредсказуемой
латентностью (кто-то ответит за 20мс, кто-то будет висеть до таймаута). Если
гонять это внутри процесса API — она будет соревноваться с обслуживанием
HTTP-запросов за горутины и усложнит graceful shutdown API под нагрузкой.
Отдельный процесс масштабируется, деплоится и падает независимо: можно
держать одну реплику `checker` на десять реплик `api`, или вообще не
запускать `checker` — сервис останется полностью рабочим, просто у всех
ссылок `status` будет `unknown`.

**How it works:**

1. `shortener.Service.Create` (если сконфигурирован `WithHealthScheduler`)
   создаёт строку в `link_health` со сроком проверки "сейчас" —
   best-effort, ошибка планирования не роняет создание ссылки.
2. `checker.Service.RunOnce` (по тикеру в `cmd/checker`) вызывает
   `HealthRepository.ClaimDue`, которая внутри Postgres делает
   `SELECT ... FOR UPDATE SKIP LOCKED` на просроченные строки **в короткой
   транзакции**, сразу сдвигает их `next_check_at` вперёд на `leaseFor`
   (аренда) и коммитит — блокировка держится миллисекунды, не всё время
   HTTP-проверки.
3. Реальные HTTP-запросы (`httpprobe.Prober`) идут уже вне транзакции,
   параллельно, но не более `WithConcurrency` штук одновременно.
4. Результат каждой проверки пишется отдельным `UPDATE` (`Record`) — успех
   планирует следующую проверку через фиксированный интервал
   (`WithUpInterval`), неудача — с экспоненциальным backoff
   (`WithDownInterval * 2^(fails-1)`, капается `WithMaxDownInterval`).

Если реплика `checker` упадёт посреди проверки — арендованные строки просто
станут снова доступны для захвата после истечения `leaseFor`, без отдельной
задачи на очистку "зависших" проверок.

## Наблюдаемость

### Метрики

И `cmd/api`, и `cmd/checker` отдают `/metrics` в формате Prometheus (у
`api` — на основном порту, у `checker` — на отдельном `METRICS_ADDR`,
потому что у него до этого вообще не было HTTP-сервера). Метрики всегда
включены — не за флагом, в отличие от кэша/rate limiting/трейсинга.

Ключевая архитектурная деталь: **все Prometheus-зависимые типы вынесены в
отдельные подпакеты**, а в основных пакетах остаются только маленькие
интерфейсы:

| Интерфейс (без внешних зависимостей) | Реализация на Prometheus |
|---|---|
| `middleware.HTTPMetricsRecorder` | `middleware/httpmetrics` |
| `checker.MetricsRecorder` | `checker/checkermetrics` |
| `shortener.CacheMetricsRecorder` | `shortener/shortenermetrics` |

Причина: если бы `*Metrics` на Prometheus жил прямо в
пакете `middleware` (или `checker`, или `shortener`), импорт
`client_golang` поломал бы сборку **всего пакета** — включая `Auth`,
`RateLimit`, доменную логику checker'а, cache-aside в `Resolve`. С
интерфейсом внутри пакета и адаптером снаружи ломается только сам адаптер;
`go test ./internal/delivery/httpapi/middleware/` по-прежнему проходит
целиком, метрики проверяются через фейковый `HTTPMetricsRecorder`. Это тот
же приём, что и `handler.Pinger`/`ratelimit.Limiter`.

Основные метрики:
- `http_requests_total{method,route,status}`, `http_request_duration_seconds{method,route}`
  — `route` это **паттерн маршрута** (`"GET /v1/links/{code}"`): иначе каждый конкретный код стал бы собственным значением лейбла и
  кардинальность росла бы вместе с количеством ссылок. `/healthz`,
  `/readyz`, `/metrics` намеренно не размечены — иначе probes от
  Kubernetes забивали бы дашборды шумом.
- `shortener_cache_lookups_total{outcome}` — `hit`/`negative_hit`/`miss`/`error`,
  прямо считает cache hit ratio.
- `checker_checks_total{result}`, `checker_check_duration_seconds`,
  `checker_claimed_batch_size`.
- Плюс стандартные Go/process коллекторы (`go_goroutines`, `go_gc_duration_seconds`,
  `process_resident_memory_bytes`, ...) — из `internal/observability.NewRegistry()`.

### Трейсинг

OpenTelemetry, экспорт по OTLP/HTTP — опционально, включается
`OTEL_EXPORTER_OTLP_ENDPOINT`. В `docker-compose.yml` за ним стоит Jaeger
(UI на `:16686`). Как и с метриками, `internal/delivery/httpapi` не
импортирует OpenTelemetry напрямую — HTTP-инструментация (`otelhttp`)
оборачивает уже собранный роутер в `cmd/api`, а не встроена в сам пакет.
В `cmd/checker` трейсинг — это декоратор `tracingProber` вокруг
`checker.Prober`, тоже собранный на месте, а не внутри пакета `checker`.

### Grafana

`deployments/docker/grafana/dashboards/urlshortener.json` — дашборд из
6 панелей (request rate по маршрутам, p50/p99 латентность, 5xx error rate,
cache hit ratio, checker checks по результату, checker duration),
подключается автоматически через provisioning при `make docker-up`
(`localhost:3000`, анонимный доступ включён для дев-стенда). Датасорс
(Prometheus) тоже прописывается сам, руками ничего добавлять не нужно.

## Установка зависимостей

```bash
go get github.com/jackc/pgx/v5@v5.10.0
go get github.com/pressly/goose/v3@v3.27.3
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
go get google.golang.org/grpc@v1.80.0
go get github.com/prometheus/client_golang@v1.20.5
go get go.opentelemetry.io/otel@v1.31.0
go get go.opentelemetry.io/otel/sdk@v1.31.0
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@v1.31.0
go get go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.56.0
go mod tidy
```

Для `internal/ratelimit/grpcclient` дополнительно нужна кодогенерация — см.
раздел "Rate limiting" выше, это отдельный шаг перед `go mod tidy`.

## Запуск

```bash
make build      # go build ./...
make test       # go test ./... -race -cover  (in-memory + httpapi + auth + rediscache/miniredis)
make vet        # go vet ./...
make fmt-check  # проверка форматирования

# Поднять весь стек (postgres -> redis -> migrate -> api + checker, плюс
# jaeger/prometheus/grafana) через docker-compose, с дев-ключом "dev-key":
make docker-up      # api :8080, checker metrics :9090, jaeger UI :16686,
                     # prometheus :9091, grafana :3000 (анонимный доступ)
make docker-down    # погасить и снести volume

curl -X POST localhost:8080/v1/links \
  -H 'X-API-Key: dev-key' -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}'
curl localhost:8080/v1/links/<code>/health   # статус доступности, после того как checker хотя бы раз пройдётся
curl localhost:8080/metrics                  # метрики api
curl localhost:9090/metrics                  # метрики checker'а (отдельный порт)

# Или руками: postgres + redis, миграции, локальный API + checker
docker compose -f deployments/docker/docker-compose.yml up -d postgres redis
export DATABASE_URL="postgres://urlshortener:urlshortener@localhost:5432/urlshortener?sslmode=disable"
export REDIS_ADDR="localhost:6379"            # опционально — без неё кэш выключен
export API_KEYS="dev-key:dev"                 # обязательно — без ключей все POST/DELETE вернут 401
export RATE_LIMITER_ADDR="localhost:50051"    # опционально — без него rate limiting выключен
export OTEL_EXPORTER_OTLP_ENDPOINT="localhost:4318"  # опционально — без него трейсинг выключен
go run ./cmd/migrate
make run           # go run ./cmd/api, слушает :8080 (в т.ч. /metrics)
make run-checker   # go run ./cmd/checker, отдельный терминал; /metrics на :9090

# Интеграционные тесты (сами поднимают/гасят контейнер Postgres через testcontainers):
make test-integration
```

## Архитектурные решения

- **Слои**: `domain` не знает о HTTP/SQL/Redis; `usecase` зависит только от
  интерфейсов (`domain.LinkRepository`, `domain.CodeGenerator`,
  `domain.LinkCache`); адаптеры (`internal/repository/memory`,
  `internal/repository/postgres`, `internal/repository/rediscache`)
  реализуют эти интерфейсы, не меняя их — переключение между реализациями в
  usecase-слое не требует правок.
- **Генерация кода**: счётчик (`CodeGenerator.Next`) + `base62.Encode`, а не
  хэш от URL — гарантирует уникальность без ретраев на коллизиях. В `memory`
  это атомарный счётчик (`sync/atomic`), в `postgres` — последовательность
  `link_codes` (`SELECT nextval(...)`), что даёт уникальность и при
  нескольких репликах API без блокировок на уровне приложения.
- **Драйвер БД — pgx v5, активно поддерживаемый стандарт
  де-факто для Go+Postgres, с нативным пулом (`pgxpool`) и быстрым
  протоколом. Миграции идут через `database/sql` (`pgx/v5/stdlib`),
  потому что `goose` рассчитан на этот интерфейс — рантайм-запросы и
  миграции сознательно используют разные подключения.
- **Миграции — goose с `go:embed`**: SQL-файлы лежат рядом с кодом в
  `internal/repository/postgres/migrations` и встраиваются в бинарник, а не
  читаются с диска в рантайме — это убирает целый класс проблем с
  деплоем "забыли скопировать папку миграций".
- **Ошибки Postgres → доменные**: `unique_violation` (23505) → `ErrLinkExists`,
  `pgx.ErrNoRows` → `ErrLinkNotFound`, `RowsAffected() == 0` на UPDATE →
  `ErrLinkNotFound`. Usecase-слой работает с одними и теми же sentinel-ошибками
  независимо от реализации репозитория.
- **Время как зависимость**: `Service` принимает `Clock` через функциональную
  опцию (`WithClock`), а не вызывает `time.Now()` напрямую — тесты на TTL
  детерминированы, без `time.Sleep`.
- **Роутер — стандартный `net/http` (Go 1.22+), не `chi`/`gorilla`**: с
  паттернами вида `"POST /v1/links"` и `r.PathValue("code")` в `ServeMux`
  сторонний роутер для четырёх маршрутов не нужен — меньше зависимостей,
  меньше поверхность для уязвимостей.
- **`handler.Pinger` — маленький интерфейс на стороне потребителя**: пакет
  `delivery/httpapi` не импортирует `pgx` напрямую, только объявляет
  `Ping(ctx) error`. `*pgxpool.Pool` ему соответствует неявно. Поэтому весь
  HTTP-слой компилируется и тестируется без единой внешней зависимости —
  собран и прогнан прямо в этой песочнице.
- **`Get` и `Resolve` — разное поведение сознательно**: публичный редирект
  (`GET /r/{code}`) использует `Resolve`, который прячет неактивные и
  просроченные ссылки (404/410). Хендлер метаданных (`GET /v1/links/{code}`)
  использует `Get` — владелец должен видеть, что ссылка деактивирована, а не
  получать голый 404, как посторонний.
- **Request ID без стороннего uuid-пакета**: `crypto/rand` + `hex` на 8 байт
  достаточно для корреляции логов одного запроса; можно подтянуть `google/uuid` с Go 1.27 
- **Порядок middleware**: `RequestID → Logging → Recover → mux`. `Recover`
  должен быть ближе к `mux`, чтобы ловить паники хендлеров; `Logging` — снаружи
  от `Recover`, чтобы залогировать финальный статус (500) даже после
  восстановленной паники, а не то, что успело записаться до неё.
- **Кэш опционален и живёт за интерфейсом `domain.LinkCache`**: появился
  только на этом этапе, когда реально понадобился (не заранее "про запас").
  Без `WithCache` `Resolve` работает как раньше, прямо в репозиторий — кэш
  это оптимизация, а не обязательная часть контракта.
- **Ошибки кэша никогда не роняют запрос**: `Get`/`IsMissing` при ошибке
  логируются (`s.logger.Warn`) и трактуются как промах — сервис идёт в
  Postgres. Redis здесь — ускоритель, а не источник истины; его недоступность
  не должна превращаться в 500-е для всего сервиса.
- **Negative caching**: промах по коду тоже кэшируется (`SetMissing`/
  `IsMissing`, короткий TTL по умолчанию — минута), иначе долбёж
  несуществующими кодами полностью обходит кэш и бьёт по БД напрямую
  (classic cache penetration).
- **Инвалидация при записи, а не только TTL**: `Create` и `Deactivate` чистят
  кэш (`Invalidate`) сразу, а не ждут, пока истечет TTL. Без этого
  деактивированная ссылка ещё до часа продолжала бы редиректить по старому
  URL, а созданная под алиасом, который до этого негативно закэшировался бы,
  ещё минуту казалась бы несуществующей.
- **TTL кэша обрезается сроком жизни самой ссылки**: `effectiveCacheTTL`
  берёт `min(cacheTTL, до ExpiresAt)`, посчитанный через инжектированный
  `Clock`, а не `time.Until` — иначе с фиксированными часами в тестах (и
  потенциально при рассинхроне часов в проде) число считалось бы неверно.
  Без этой обрезки ссылка с TTL 30 секунд могла бы отдаваться из кэша ещё
  час после истечения.
- **`internal/repository/rediscache`, а не `internal/repository/redis`**: та
  же причина, что и с `httpapi` — иначе в `cache.go` конфликтовали бы
  идентификаторы своего пакета и импортированного `github.com/redis/go-redis/v9`
  (у него тоже пакет называется `redis`).
- **Позитивные и негативные записи — разные ключи**, не значение-«tombstone» в
  одном месте: `Invalidate` тогда чистит оба одним `DEL` без необходимости
  сперва проверять, что там лежит.
- **`OwnerID` — из `Principal`, не из тела запроса**: `CreateLinkRequest` DTO
  никогда не имел поля `owner_id` — иначе любой клиент мог бы создавать
  ссылки от чужого имени. Владелец определяется тем, каким `X-API-Key`
  пришёл запрос, и всё.
- **`Deactivate` проверяет владельца, не только валидность ключа**: без этой
  проверки любой валидный API-ключ мог бы деактивировать чужие ссылки — 401
  (аутентификация) и 403 (авторизация) здесь осознанно разные коды.
- **Auth обязателен, rate limiting и кэш — опциональны**: `KeyStore` в
  `NewRouter` не `nil`-able — пустой `StaticKeyStore{}` (если `API_KEYS` не
  задан) просто отклоняет всё, а не отключает проверку. Это разные вещи:
  забыть выдать ключи — не то же самое, что решить не проверять их.
- **Rate limiter — через порт `ratelimit.Limiter`, транспорт (gRPC) спрятан
  за ним**: middleware и роутер видят только `Allow(ctx, key) (Decision, error)`
  — так же, как `handler.Pinger` прячет `pgx`/`redis`. `internal/ratelimit/grpcclient`
  — единственное место, которое знает про `google.golang.org/grpc`.
- **Ошибки лимитера не блокируют трафик (fail open)**: тот же принцип, что и
  с кэшем — недоступность защитного слоя не должна превращаться в отказ
  всего API. Осознанный компромисс: под реальной атакой отказавший
  rate limiter временно снимает защиту, а не останавливает сервис.
- **Ключ для лимита разный на разных маршрутах**: `POST /v1/links` лимитится
  по `PrincipalKey` (аутентифицированный `OwnerID` — у каждого ключа свой
  бюджет), `GET /r/{code}` — по `ClientIPKey` (маршрут публичный, личности
  нет). `PrincipalKey` не может использоваться раньше `Auth` в цепочке — он
  читает `Principal` из контекста, который `Auth` туда кладёт.
- **Двухфазный `ClaimDue`, а не одна долгая транзакция**: `SELECT ... FOR
  UPDATE SKIP LOCKED` + `UPDATE next_check_at` коммитятся за миллисекунды;
  сам HTTP-запрос к целевому URL идёт уже без открытой транзакции. Держать
  транзакцию (и, соответственно, подключение из пула) открытой на всё время
  сетевого запроса с непредсказуемой латентностью — способ быстро исчерпать
  пул соединений под нагрузкой.
- **Lease вместо отдельной задачи на "зависшие" проверки**: если `checker`
  упал посреди HTTP-запроса, разлоченная транзакцией строка не остаётся
  залоченной навечно — она просто станет `next_check_at <= now` снова после
  истечения `leaseFor`, и её заберёт следующий `RunOnce` (тот же процесс
  или другая реплика). Не нужен ни cron на очистку, ни ручное вмешательство.
- **Backoff считает usecase, не SQL**: `consecutive_fails` и `next_check_at`
  вычисляются в Go (`checker.Service.nextCheckAt`) и передаются в `Record`
  готовыми значениями, а не через `CASE WHEN` в `UPDATE`. Строка уже
  эксклюзивно захвачена этим воркером через lease, гонки нет — но бизнес-
  правило "как долго ждать после N неудач" — это то, что должно быть
  протестировано юнит-тестами на Go, а не проверено вручную через SQL.
- **`checker` — отдельный бинарник, не горутина в `cmd/api`**: раздел
  "Checker-сервис" выше объясняет почему (I/O-bound работа с непредсказуемой
  латентностью против произвольных серверов не должна конкурировать с API
  за горутины и усложнять его graceful shutdown).
- **`HealthGetter` называется как метод `domain.HealthRepository`**: интерфейс
  в `handler` объявляет `GetByCode`, а не, скажем, `GetHealth` — тогда
  `*postgres.HealthRepo` подходит под него без адаптера, структурная
  типизация делает всю работу. `cmd/api` строит `HealthRepo` один раз и
  передаёт его и в `shortener.WithHealthScheduler` (запись), и в роутер
  (чтение) — два разных использования одного и того же адаптера.
- **Проверка доступности не входит в `Resolve`**: `GET /r/{code}` не ждёт
  и не блокируется на состоянии `checker` — редирект работает на основе
  `links`, `link_health` — отдельная, необязательная для самого редиректа
  таблица. Сайт может быть помечен как `down` и продолжать редиректить —
  это осознанно: пользователь должен решать, довериться ли статусу, а не
  быть заблокированным middleware, который сам может ошибаться.
- **Метрики — за интерфейсом, реализация в отдельном подпакете**: раздел
  "Наблюдаемость" выше объясняет почему (иначе импорт `client_golang`
  ломает сборку всего пакета-хозяина, а не только адаптера). Тот же приём,
  доведённый до логического предела, что и `handler.Pinger`/
  `ratelimit.Limiter`/`shortener.HealthScheduler` — маленький интерфейс на
  стороне потребителя, конкретная реализация собирается в `cmd/*`.
- **`route`, а не `r.URL.Path`, в лейбле HTTP-метрик**: Go 1.22 `ServeMux`
  не отдаёт хендлеру смэтченный паттерн через `*http.Request` (проверено —
  такого поля там нет), так что вместо попытки его как-то восстановить,
  `route` передаётся явно в момент регистрации каждого маршрута
  (`deps.wrapMetrics("GET /v1/links/{code}", ...)`). Тот же путь, что
  используется в `mux.Handle`, буквально копируется в лейбл — не может
  разъехаться.
- **`/healthz`, `/readyz`, `/metrics` не размечены HTTP-метриками**: это
  scrape/probe-трафик, а не пользовательский — если считать его наравне с
  остальным, дашборды забиваются шумом от k8s-проб и от самого Prometheus,
  дёргающего `/metrics` каждые 15 секунд.
- **`internal/observability` — только реестр и настройка трейсера, не сами
  метрики**: соблазн был сложить всё "наблюдаемое" в один пакет, но тогда
  `NewRegistry()` (нужен почти всем) тянул бы за собой то, что нужно не
  всем. Метрики остаются рядом с кодом, который их производит — тот же
  принцип локальности, что и с доменными портами.
- **HTTP-инструментация трейсинга (`otelhttp`) оборачивает роутер в
  `cmd/api`, не встроена в `httpapi`**: тот же паттерн, что с
  `promhttp`/`httpmetrics` — пакет транспорта не должен решать, каким
  трейсером или экспортёром пользоваться; это решение уровня сборки
  процесса, а не библиотеки.
- **Grafana-дашборд — provisioning**: датасорс и
  сам дашборд подключаются автоматически через файлы в
  `grafana/provisioning`, смонтированные read-only в контейнер — после
  `make docker-up` в Grafana сразу есть рабочие панели, а не пустой
  экран с инструкцией "добавь источник данных".
