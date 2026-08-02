# urlshortener

Учебно-портфолийный URL shortener на Go: domain-driven архитектура, кэширование,
асинхронная проверка доступности целевых ссылок. Полный план — в `docs/` (будет
добавлен по мере реализации следующих этапов).

## Статус: этап 1 — domain + in-memory MVP

Реализовано:
- `internal/domain` — сущность `Link`, доменные ошибки, порты (`LinkRepository`,
  `CodeGenerator`)
- `pkg/base62` — кодирование/декодирование коротких кодов
- `internal/usecase/shortener` — бизнес-логика: `Create`, `Resolve`, `Deactivate`
- `internal/repository/memory` — in-memory реализации портов (заглушки для
  будущих Postgres/Redis адаптеров)

Ещё не реализовано (следующие этапы): HTTP-слой, Postgres, Redis-кэш,
rate limiting, checker-сервис, очередь событий, наблюдаемость.

## Запуск

```bash
make build      # go build ./...
make test       # go test ./... -race -cover
make vet        # go vet ./...
make fmt-check  # проверка форматирования
```

## Архитектурные решения

- **Слои**: `domain` не знает о HTTP/SQL; `usecase` зависит только от
  интерфейсов (`domain.LinkRepository`, `domain.CodeGenerator`); адаптеры
  (`internal/repository/memory`, позже `postgres`/`redis`) реализуют эти
  интерфейсы.
- **Генерация кода**: счётчик (`CodeGenerator.Next`) + `base62.Encode`, а не
  хэш от URL — гарантирует уникальность без ретраев на коллизиях. В `memory`
  счётчик атомарный (`sync/atomic`), в проде это будет Postgres sequence или
  Redis `INCR`.
- **Время как зависимость**: `Service` принимает `Clock` через функциональную
  опцию (`WithClock`), а не вызывает `time.Now()` напрямую — тесты на TTL
  детерминированы, без `time.Sleep`.
- **Ошибки**: доменные sentinel-ошибки (`domain.ErrLinkNotFound` и т.д.),
  оборачиваются через `fmt.Errorf("...: %w", err)`, проверяются в тестах через
  `errors.Is`.
