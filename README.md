# urlshortener

Учебный URL shortener на Go: domain-driven архитектура, кэширование,
асинхронная проверка доступности целевых ссылок. Полный план — в `docs/` (будет
добавлен по мере реализации следующих этапов).

## Статус: этап 2 — Postgres

Реализовано:
- `internal/domain` — сущность `Link`, доменные ошибки, порты (`LinkRepository`,
  `CodeGenerator`)
- `pkg/base62` — кодирование/декодирование коротких кодов
- `internal/usecase/shortener` — бизнес-логика: `Create`, `Resolve`, `Deactivate`
- `internal/repository/memory` — in-memory реализации портов (для юнит-тестов
  usecase-слоя, без реальной БД)
- `internal/repository/postgres` — реализации тех же портов на **pgx v5**:
  `LinkRepo` (таблица `links`), `CodeGen` (последовательность `link_codes`),
  плюс `Migrate()` — встроенные через `go:embed` **goose**-миграции
- `cmd/migrate` — отдельный бинарник для наката миграций (`DATABASE_URL` из env)
- `test/integration` — интеграционные тесты на реальном Postgres через
  **testcontainers-go** (сами поднимают и гасят контейнер; нужен локальный
  Docker), под build tag `integration`, не запускаются обычным `go test ./...`
- `deployments/docker/docker-compose.yml` — Postgres для ручной проверки

Ещё не реализовано (следующие этапы): HTTP-слой, Redis-кэш, rate limiting,
checker-сервис, очередь событий, наблюдаемость.

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
make test       # go test ./... -race -cover  (только in-memory, без БД)
make vet        # go vet ./...
make fmt-check  # проверка форматирования

# Поднять Postgres локально и накатить миграции:
docker compose -f deployments/docker/docker-compose.yml up -d
DATABASE_URL="postgres://urlshortener:urlshortener@localhost:5432/urlshortener?sslmode=disable" \
  go run ./cmd/migrate

# Интеграционные тесты (сами поднимают/гасят контейнер Postgres через testcontainers):
go test -tags=integration ./test/integration/... -v
```

## Архитектурные решения

- **Слои**: `domain` не знает о HTTP/SQL; `usecase` зависит только от
  интерфейсов (`domain.LinkRepository`, `domain.CodeGenerator`); адаптеры
  (`internal/repository/memory`, `internal/repository/postgres`, позже
  `redis`) реализуют эти интерфейсы, не меняя их — переключение между
  in-memory и Postgres в usecase-слое не требует правок.
- **Генерация кода**: счётчик (`CodeGenerator.Next`) + `base62.Encode`, а не
  хэш от URL — гарантирует уникальность без ретраев на коллизиях. В `memory`
  это атомарный счётчик (`sync/atomic`), в `postgres` — последовательность
  `link_codes` (`SELECT nextval(...)`), что даёт уникальность и при
  нескольких репликах API без блокировок на уровне приложения.
- **Драйвер БД — pgx v5 `pgx/v5` — активно поддерживаемый стандарт
  де-факто для Go+Postgres, с нативным пулом (`pgxpool`) и быстрым
  протоколом. Миграции всё равно идут через `database/sql` (`pgx/v5/stdlib`),
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
