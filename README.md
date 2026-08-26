# urlshortener

URL shortener на Go: domain-driven архитектура, кэширование,
асинхронная проверка доступности целевых ссылок.

## Статус: этап 6 — checker-сервис (проверка доступности)

Реализовано:
- `internal/domain` — сущность `Link`, доменные ошибки, порты (`LinkRepository`,
  `CodeGenerator`, `LinkCache`, `HealthRepository`); типы `LinkHealth`,
  `DueCheck`, `HealthResult` для availability-чеков
- `pkg/base62` — кодирование/декодирование коротких кодов
- `internal/usecase/shortener` — бизнес-логика: `Create`, `Get`, `Resolve`,
  `Deactivate`, cache-aside через `domain.LinkCache` (опционально), плюс
  планирование первой проверки доступности через `HealthScheduler`
  (опционально) — `Create` просит checker её проверить
- `internal/usecase/checker` — вторая бизнес-логика проекта: `RunOnce`
  забирает пачку просроченных проверок (`ClaimDue`), гоняет их через
  worker pool с ограниченной конкурентностью, пишет результат с фиксированным
  интервалом (успех) или экспоненциальным backoff с капом (неудача).
  `httpprobe` — реальный HTTP-пробер (GET, таймаут, 5xx = down)
- `internal/repository/memory`, `internal/repository/postgres`,
  `internal/repository/rediscache` — реализации портов домена (in-memory,
  Postgres/pgx v5, Redis/go-redis v9); `postgres.HealthRepo` — двухфазный
  `ClaimDue` (`SELECT ... FOR UPDATE SKIP LOCKED` в короткой транзакции +
  lease, без удержания блокировки на время самого HTTP-запроса)
- `internal/auth` — API-ключи: `Principal`, `KeyStore`, `StaticKeyStore`
  (`API_KEYS=key:owner[:name]` из env), контекст-хелперы
- `internal/ratelimit` — порт `Limiter` (не завязан на транспорт) +
  `grpcclient` — клиент к rate limiter'у по gRPC
  (контракт — `api/ratelimit/v1/ratelimit.proto`, см. раздел "Rate limiting"
  ниже)
- `internal/config` — конфиг из env-переменных, без внешних зависимостей.
  Кэш и rate limiting — опциональны (`REDIS_ADDR`/`RATE_LIMITER_ADDR`), auth
  — обязателен: `POST`/`DELETE /v1/links` всегда за API-ключом
- `internal/delivery/httpapi` — роутер на `net/http` 1.22+, общая цепочка
  middleware (request id → logging → recover) плюс точечные обёртки на
  конкретных маршрутах (auth, rate limit — не на всех сразу, см. таблицу
  эндпоинтов), хендлеры, DTO — `Create` берёт `OwnerID` из аутентифицированного
  `Principal` (не из тела запроса), `Deactivate` дополнительно проверяет
  владельца ссылки
- `cmd/api` — сборка сервиса: config → Postgres → (опц.) Redis → (опц.) rate
  limiter → usecase → HTTP-сервер с graceful shutdown; readiness проверяет
  все подключённые зависимости
- `cmd/checker` — тикер по `CHECKER_INTERVAL_SECONDS`
  → `checker.Service.RunOnce`. Не часть `cmd/api` — см. раздел "Checker-сервис"
  ниже
- `cmd/migrate` — отдельный бинарник для наката миграций (`DATABASE_URL` из env)
- `test/integration` — интеграционные тесты на реальном Postgres через
  **testcontainers-go** (build tag `integration`, локальный Docker)
- `deployments/docker` — `Dockerfile.api` (собирает `api`+`migrate`+`checker`)
  + `docker-compose.yml`: postgres → redis → одноразовый `migrate` →
  `api` + `checker` (с дев-ключом `dev-key` из коробки)

Ещё не реализовано (следующие этапы): очередь событий, наблюдаемость
(Prometheus/Grafana/трейсинг).

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

`X-API-Key` берётся из `API_KEYS` (см. ниже). `DELETE` дополнительно проверяет,
что ключ принадлежит владельцу ссылки — иначе `403`, не только `401`.

## Rate limiting

`POST /v1/links` (по владельцу) и `GET /r/{code}` (по IP) лимит через
`internal/ratelimit.Limiter` — gRPC-клиентом к
отдельному GCRA rate limiter'у.

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# и сам protoc: https://protobuf.dev/installation/

make proto   # генерирует internal/ratelimit/grpcclient/ratelimitv1
go get google.golang.org/grpc@v1.80.0
go mod tidy
```

Без `RATE_LIMITER_ADDR` rate limiting просто выключен — тот же принцип, что и с `REDIS_ADDR`.

## Checker-сервис

`cmd/checker` — отдельный бинарник и отдельный процесс. 
Проверка доступности — это
I/O-bound работа против произвольных внешних серверов с непредсказуемой
латентностью (кто-то ответит за 20мс, кто-то будет висеть до таймаута). Если
гонять это внутри процесса API — она будет соревноваться с обслуживанием
HTTP-запросов за горутины и усложнит graceful shutdown API под нагрузкой.
Отдельный процесс масштабируется, деплоится и падает независимо: можно
держать одну реплику `checker` на десять реплик `api`, или вообще не
запускать `checker` — сервис останется полностью рабочим, просто у всех
ссылок `status` будет `unknown`.

**Как реализовано:**

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
   (`WithDownInterval * 2^(fails-1)`, капается `WithMaxDownInterval`), чтобы
   мёртвый сайт не стучался с постоянной частотой вечно.

Если реплика `checker` упадёт посреди проверки — арендованные строки просто
станут снова доступны для захвата после истечения `leaseFor`, без отдельной
задачи на очистку "зависших" проверок.

## Установка зависимостей

```bash
go get github.com/jackc/pgx/v5@v5.10.0
go get github.com/pressly/goose/v3@v3.27.3
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
go get google.golang.org/grpc@v1.80.0
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

# Поднять весь стек (postgres -> redis -> migrate -> api + checker) через
# docker-compose, с дев-ключом "dev-key" из коробки:
make docker-up      # http://localhost:8080
make docker-down    # погасить и снести volume

curl -X POST localhost:8080/v1/links \
  -H 'X-API-Key: dev-key' -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}'
curl localhost:8080/v1/links/<code>/health   # статус доступности, после того как checker хотя бы раз пройдётся

# Или руками: postgres + redis, миграции, локальный API + checker
docker compose -f deployments/docker/docker-compose.yml up -d postgres redis
export DATABASE_URL="postgres://urlshortener:urlshortener@localhost:5432/urlshortener?sslmode=disable"
export REDIS_ADDR="localhost:6379"            # опционально — без неё кэш выключен
export API_KEYS="dev-key:dev"                 # обязательно — без ключей все POST/DELETE вернут 401
export RATE_LIMITER_ADDR="localhost:50051"    # опционально — без него rate limiting выключен
go run ./cmd/migrate
make run                    # go run ./cmd/api, слушает :8080
go run ./cmd/checker &      # отдельный процесс; без него /health всегда "unknown"

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
  достаточно для корреляции логов одного запроса; можно подтянуть `google/uuid` с 1.27.
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
  кэш (`Invalidate`) сразу, а не ждут, пока пройдёт TTL. Без этого
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
  транзакцию (и, соответственно, соединение из пула) открытой на всё время
  сетевого запроса с непредсказуемой латентностью — способ быстро исчерпать
  пул соединений под нагрузкой.
- **Lease вместо отдельной задачи на "зависшие" проверки**: если `checker`
  упал посреди HTTP-запроса, разлоченная транзакцией строка не остаётся
  залоченной навечно — она просто станет `next_check_at <= now` снова после
  истечения `leaseFor`, и её заберёт следующий `RunOnce` (тот же процесс
  или другая реплика). Не нужен ни cron на очистку, ни ручное вмешательство.
- **Backoff считает usecase, не SQL**: `consecutive_fails` и `next_check_at`
  вычисляются в Go (`checker.Service.nextCheckAt`) и передаются в `Record`
  готовыми значениями. Строка уже
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
