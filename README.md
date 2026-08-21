# urlshortener

URL shortener на Go: domain-driven архитектура, кэширование,
асинхронная проверка доступности целевых ссылок.

## Статус: этап 4 — Redis cache-aside

Реализовано:
- `internal/domain` — сущность `Link`, доменные ошибки, порты (`LinkRepository`,
  `CodeGenerator`, `LinkCache`)
- `pkg/base62` — кодирование/декодирование коротких кодов
- `internal/usecase/shortener` — бизнес-логика: `Create`, `Get`, `Resolve`,
  `Deactivate`. `Resolve` теперь умеет cache-aside: если передан кэш
  (`WithCache`), сначала смотрит в него (включая negative cache — "точно
  не существует"), и только при промахе идёт в репозиторий; `Create`/
  `Deactivate` инвалидируют кэш. Кэш полностью опционален — без `WithCache`
  сервис работает ровно как на этапе 3
- `internal/repository/memory` — in-memory реализации портов (для юнит-тестов
  usecase-слоя, без реальной БД)
- `internal/repository/postgres` — реализации портов на **pgx v5**:
  `LinkRepo` (таблица `links`), `CodeGen` (последовательность `link_codes`),
  плюс `Migrate()` — встроенные через `go:embed` **goose**-миграции
- `internal/repository/rediscache` — `Cache` на **go-redis v9**: позитивные
  и негативные (`SetMissing`/`IsMissing`) записи отдельными ключами с TTL,
  `Invalidate` чистит оба одним `DEL`
- `internal/config` — конфиг из env-переменных, без внешних зависимостей.
  Кэш включается только если задан `REDIS_ADDR`
- `internal/delivery/httpapi` — роутер на `net/http` 1.22+ (`ServeMux` с
  паттернами вида `"POST /v1/links"`), цепочка middleware (request id →
  logging → recover), хендлеры, DTO отдельно от домена — целиком на
  стандартной библиотеке, без стороннего роутера
- `cmd/api` — сборка сервиса: config → Postgres pool → (опционально) Redis →
  usecase → HTTP-сервер с graceful shutdown по `SIGTERM`/`SIGINT`; readiness
  проверяет и Postgres, и Redis, если он включён
- `cmd/migrate` — отдельный бинарник для наката миграций (`DATABASE_URL` из env)
- `test/integration` — интеграционные тесты на реальном Postgres через
  **testcontainers-go** (сами поднимают и гасят контейнер; нужен локальный
  Docker), под build tag `integration`, не запускаются обычным `go test ./...`
- `deployments/docker` — `Dockerfile.api` (multi-stage) + `docker-compose.yml`
  с полным стеком: postgres → redis → одноразовый `migrate` → `api`

Ещё не реализовано (следующие этапы): rate limiting, checker-сервис, очередь
событий, наблюдаемость (Prometheus/Grafana/трейсинг).

### Эндпоинты

| Метод | Путь | Что делает |
|---|---|---|
| `POST` | `/v1/links` | создать короткую ссылку |
| `GET` | `/v1/links/{code}` | метаданные ссылки (видно и неактивные/просроченные) |
| `DELETE` | `/v1/links/{code}` | деактивировать |
| `GET` | `/r/{code}` | редирект 302 (404/410, если ссылки нет/неактивна/просрочена) |
| `GET` | `/healthz` | liveness |
| `GET` | `/readyz` | readiness (пингует БД, и Redis, если он включён) |

## Установка зависимостей

```bash
go get github.com/jackc/pgx/v5@v5.10.0
go get github.com/pressly/goose/v3@v3.27.3
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
go mod tidy
```

## Запуск

```bash
make build      # go build ./...
make test       # go test ./... -race -cover  (in-memory + httpapi + rediscache/miniredis, без реальной БД)
make vet        # go vet ./...
make fmt-check  # проверка форматирования

# Поднять весь стек (postgres -> redis -> migrate -> api) через docker-compose:
make docker-up      # http://localhost:8080
make docker-down    # погасить и снести volume

# Или руками: postgres + redis, миграции, локальный API
docker compose -f deployments/docker/docker-compose.yml up -d postgres redis
export DATABASE_URL="postgres://urlshortener:urlshortener@localhost:5432/urlshortener?sslmode=disable"
export REDIS_ADDR="localhost:6379"   # опционально — без неё кэш просто выключен
go run ./cmd/migrate
make run    # go run ./cmd/api, слушает :8080

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
- **Драйвер БД — pgx v5, не `pgx/v5` — активно поддерживаемый стандарт
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
  достаточно для корреляции логов одного запроса; тянуть `google/uuid` ради
  этого — лишняя зависимость.
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
  кэш (`Invalidate`) сразу, а не ждут, пока протухнет TTL. Без этого
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
