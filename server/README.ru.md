# Synapse — бэкенд мессенджера уровня Telegram (Go)

Production-подобный MVP платформы обмена сообщениями в реальном времени:
**собственный бинарный протокол** поверх TCP, WebSocket и **QUIC**,
**мультиузловой realtime-шлюз** и доменные **микросервисы**, связанные через
**шину событий на JetStream**, с хранением в Postgres/Redis/NATS и **CLI-клиентом на Go**.

Это работающее ядро системы, спроектированной в [ARCHITECTURE.md](ARCHITECTURE.md)
(отвечает на все 18 разделов ТЗ). Код разбит по границам сервисов, чтобы позже их
можно было вынести в отдельные развёртывания, но сегодня всё работает как один
процесс.

> **Впервые здесь?** Прочитайте [GUIDE.md](GUIDE.md) — подробный разбор всего «с
> нуля», от протокола до сквозного шифрования.

## Что работает end-to-end

- Протокол «кадр + конверт» (`pkg/wire`), **тела в protobuf** — поверх **TCP, WebSocket и QUIC**
- Рукопожатие с согласованием возможностей, авторизация, восстановление сессии (**replay-буфер в Redis**)
- Личные чаты 1-на-1 (создаются по `@username`), модель групп/каналов
- Надёжная **идемпотентная** запись с **порядком без дыр**, единый **командный брокер сообщений** (валидация/трейсинг/метрики)
- **Групповой коммит записи** — параллельные вставки в один fsync (~×16 throughput, durability сохранена)
- **Мультиузловость**: кросс-узловая доставка (реестр в Redis + шина), общий каталог предключей (Redis) и индекс поиска (Postgres tsvector)
- **Огромные чаты остаются посильными**: веерная рассылка обходит участников канала постранично (keyset), не материализуя их, а авторизация выше порога размера — точечный запрос по первичному ключу вместо кешированной копии ролей всех участников
- Веерная доставка на несколько устройств; офлайн → задание на push
- Галочки прочтения, «печатает…», онлайн/был в сети
- Редактирование/удаление (tombstone), постраничная история + **постраничный экспорт чата** (владелец/админ)
- **Полнотекстовый поиск** (с фильтром по правам), **медиа** (подписанные ссылки, AV-скан)
- **Push**, **модерация** (стоп-слова + скорость спама), **аудит-лог**, **RBAC**-роли
- **Создание групп и каналов по протоколу**, участники резолвятся из `@username`
- **Push-уведомления** по устройствам, с ретраями и снятием мёртвых токенов
- **Хранение под контролем**: outbox, отработавшие отложенные отправки и replay-буфер собираются, а медиа удаляется вместе с сообщением (или подметается, если на него никто не ссылается)
- **Реакции, треды, типизированные вложения** (голосовые, кружки, файлы, картинки) и **опросы** с живым подсчётом
- **Сигналинг звонков и конференций** (комнаты, ростер, пересылка непрозрачных SDP/ICE) — медиа идёт мимо сервера, peer-to-peer
- **Контакты и блокировки** (инкрементальная синхронизация; блокировка режет трафик в обе стороны)
- **Пересылка** с источником, который переживает удаление оригинала, **самоуничтожающиеся сообщения** (репер-надгробия), **отложенная отправка** (захват строк SKIP LOCKED, права перепроверяются в момент выстрела)
- **Закреплённые сообщения** (общие для чата, с правами админа) и **черновики**, синхронные только между своими устройствами
- **Публичные адреса чатов**, отзываемые **пригласительные ссылки** (128-битный код, лимиты по числу входов и сроку, атомарное погашение), **роли владельца/админа** с защитой последнего владельца
- **Секретные E2E-чаты**: X3DH + Double Ratchet (`pkg/e2e`), Ed25519-подписи предключей, **мультиустройственная синхронизация**; сервер видит только шифртекст
- **Транзакционный outbox** (FOR UPDATE SKIP LOCKED + LISTEN/NOTIFY) → **durable-консьюмеры JetStream**
- **Сжатие**: zstd + общий словарь (согласуется), gzip как fallback
- **QoS-полосы**: control > messages > typing/presence; эфемерное дропается под нагрузкой
- **Надёжность**: circuit breaker + локальный fallback при падении Redis; **версионированные миграции** (golang-migrate)
- **Наблюдаемость**: Prometheus-гистограммы (send→ack, лаг fanout), pprof, трейсинг OpenTelemetry (OTLP)
- **Харденинг**: TLS 1.3, хеш токенов в БД, argon2id (с лимитом конкурентности), таймауты рукопожатия/простоя,
  флуд-контроль + троттлинг брутфорса, фаззинг парсера — см. [SECURITY.md](SECURITY.md)
- Порядковые номера/подтверждения, backpressure, **один общий reaper живости** (2 горутины/conn)
- Snowflake-ID (лиз node-id из Redis); подменяемое хранилище: **в памяти** или **Postgres + Redis + NATS**

## Требования

- Go 1.26+
- (Опционально) Docker + Docker Compose для «взрослой» инфраструктуры

## Быстрый старт (без настройки, в памяти)

```bash
# терминал 1 — сервер (хранилище/шина/presence в памяти)
go run ./cmd/server

# терминал 2 — Алиса (первый раз: -register создаёт аккаунт)
go run ./cmd/client -register -user alice -pass secret123

# терминал 3 — Боб
go run ./cmd/client -register -user bob -pass secret123
```

В окне Алисы:

```
/to @bob
привет, Боб!
/hist
/search привет
```

Боб видит сообщение в реальном времени и отвечает через `/to @alice`. Флаг
`-register` — только при первом входе (создать аккаунт); дальше — без него.

### Команды клиента

Всё работает с текущим чатом — команда читается как «сделай это здесь».
Исключения: `/join` (им как раз и получают чат) и команды участия, которым
нужен конкретный id чата, а не `@user`.

| Команда                                    | Действие                                               |
|--------------------------------------------|--------------------------------------------------------|
| `/to @user`                                | выбрать личный чат с `@user`                            |
| `/to <chatID>`                             | выбрать существующий чат по id                          |
| `<текст>`                                  | отправить текст в текущий чат                           |
| `/hist [n]`                                | подгрузить последние `n` сообщений (по умолчанию 20)    |
| `/read <seq>`                              | отметить прочитанным до номера `<seq>`                  |
| `/typing`                                  | отправить индикатор «печатает…»                         |
| `/search <текст>`                          | полнотекстовый поиск по своим чатам                     |
| `/upload <путь>`                           | загрузить файл и отправить его в текущий чат            |
| `/react <msgID> <emoji>`                   | поставить/снять реакцию                                 |
| `/thread <msgID>`                          | загрузить ветку ответов                                 |
| `/forward <msgID> <chat>`                  | переслать сообщение в другой чат (с указанием источника)|
| `/ttl <секунды>`                           | самоуничтожение отправляемого дальше (`0` — выключить)  |
| `/schedule <+2h\|RFC3339> <текст>`         | отправить позже                                         |
| `/scheduled` · `/unschedule <id>`          | список / отмена отложенных отправок                     |
| `/pin <msgID>` · `/unpin <msgID>` · `/pins`| закреплённые сообщения (общие для чата)                 |
| `/draft [текст]` · `/drafts`               | черновик между устройствами (пустой текст — очистить)   |
| `/poll Q\|A\|B` · `/vote <pollID> <n>`     | создать опрос / проголосовать                           |
| `/contact @user [имя]` · `/contacts`       | добавить контакт / синхронизировать книгу               |
| `/block @user` · `/unblock @user`          | заблокировать или разблокировать (в обе стороны)        |
| `/group <название> [@user...]`             | создать группу (`/channel` — канал)                     |
| `/handle <имя\|->`                         | публичный адрес чата (только владелец)                  |
| `/invite [uses] [+24h]` · `/invites`       | создать / показать пригласительные ссылки (админ)       |
| `/revoke <code>`                           | отозвать ссылку (админ)                                 |
| `/join <code\|@handle>`                    | войти по ссылке или публичному адресу                   |
| `/role <userID> <member\|admin\|owner>`    | повысить/понизить участника (владелец)                  |
| `/call [audio\|video]`                     | начать звонок; `/accept` `/decline` `/hangup`           |
| `/chats [курсор]`                          | список своих чатов страницами                           |
| `/me` · `/who @user`                       | свой профиль / найти пользователя по handle или id      |
| `/name <имя>`                              | сменить отображаемое имя (`-` — убрать аватар)          |
| `/export <id>`                             | выгрузить участников + сообщения чата (владелец/админ)  |
| `/quit`                                    | отключиться                                             |

### Транспорты WebSocket / QUIC

Тот же протокол работает поверх WebSocket (браузер/край) и QUIC (UDP, для мобильных —
миграция соединения при смене сети):

```bash
go run ./cmd/client -ws ws://localhost:8080/ws -register -user carol -pass secret123
# QUIC (требует TLS на сервере):
SYNAPSE_TLS_SELFSIGNED=1 SYNAPSE_QUIC=1 go run ./cmd/server
go run ./cmd/client -quic -insecure -addr localhost:7000 -register -user dave -pass secret123
```

## Запуск с инфраструктурой Docker (надёжно)

```bash
docker compose up -d          # Postgres, Redis, NATS

SYNAPSE_PG_DSN="postgres://synapse:synapse@localhost:5432/synapse?sslmode=disable" \
SYNAPSE_REDIS_ADDR="localhost:6379" \
SYNAPSE_NATS_URL="nats://localhost:4222" \
go run ./cmd/server
```

Сервер сам применяет **версионированные миграции** (golang-migrate,
`internal/store/postgres/migrations/`) при старте — initdb-скрипт не нужен.

### Микросервисный флот (та же система, разнесённая)

`cmd/server` — модульный монолит. Та же система работает и как **отдельно
разворачиваемые процессы** по gRPC (sync) + NATS (async): `authd`, `chatd`,
`messaged`, `presenced`, `keydird` (gRPC-сервисы), `fanoutd`/`notifyd`/
`moderationd`/`searchd` (воркеры шины) и `gatewayd` (край, дозванивается до
сервисов gRPC-клиентами, удовлетворяющими тем же интерфейсам `gateway.Services`).
Код обработчиков шлюза между двумя вариантами не меняется.

```bash
docker compose -f deploy/microservices/docker-compose.yml up --build
go run ./cmd/client -addr localhost:7000 -register -user alice -pass secret123
```

Топология, требование общего состояния и цена сетевого хопа gRPC —
в [deploy/microservices/README.md](deploy/microservices/README.md).

### Стек наблюдаемости (опционально)

```bash
SYNAPSE_OTLP_ENDPOINT=localhost:4318 go run ./cmd/server            # слать трейсы по OTLP
docker compose -f deploy/observability/docker-compose.yml up -d     # Prometheus + Tempo + Grafana
```

Grafana на http://localhost:3000 (Explore → Prometheus / Tempo). `/metrics` отдаёт
гистограммы латентности send→ack и лага fanout; `SYNAPSE_PPROF=1` монтирует `/debug/pprof/`.

### Переменные окружения сервера

| Переменная                | По умолчанию   | Значение                                 |
|---------------------------|----------------|------------------------------------------|
| `SYNAPSE_TCP_ADDR`        | `:7000`        | слушатель бинарного протокола (raw TCP)  |
| `SYNAPSE_WS_ADDR`         | `:8080`        | WebSocket (`/ws`) + `/healthz` + `/metrics` |
| `SYNAPSE_PG_DSN`          | *(не задано)*  | DSN Postgres — включает надёжное хранение |
| `SYNAPSE_REDIS_ADDR`      | *(не задано)*  | адрес Redis — presence/router/keydir/resume |
| `SYNAPSE_NATS_URL`        | *(не задано)*  | URL NATS — шина событий (JetStream)      |
| `SYNAPSE_NODE_ID`         | *(hostname)*   | номер узла Snowflake (0–1023); иначе лиз из Redis |
| `SYNAPSE_TLS_CERT`/`_KEY` | *(не задано)*  | включить TLS 1.3 парой сертификат/ключ   |
| `SYNAPSE_TLS_SELFSIGNED`  | *(не задано)*  | `1` = самоподписанный TLS (dev)          |
| `SYNAPSE_QUIC`            | *(не задано)*  | `1` = слушать также QUIC (UDP; требует TLS) |
| `SYNAPSE_REQUIRE_TLS`     | *(не задано)*  | `1` = не стартовать без TLS (нет тихого plaintext) |
| `SYNAPSE_MAX_CONNS_PER_IP`| *(не задано)*  | лимит одновременных соединений с одного IP (антифлуд) |
| `SYNAPSE_ACCEPT_RATE_PER_IP`| *(не задано)*| лимит новых соединений/сек с одного IP (антишторм) |
| `SYNAPSE_ALLOWED_ORIGINS` | *(не задано)*  | список разрешённых origin для WebSocket  |
| `SYNAPSE_MEDIA_SECRET`    | dev-значение   | ключ HMAC для подписи медиа-ссылок        |
| `SYNAPSE_ADMIN_USERS` / `_MODERATOR_USERS` | *(не задано)* | id админов/модераторов (RBAC) |
| `SYNAPSE_TRACE` / `SYNAPSE_OTLP_ENDPOINT` | *(не задано)* | трейсинг: stdout / OTLP-коллектор |
| `SYNAPSE_PPROF`           | *(не задано)*  | `1` монтирует `/debug/pprof/`            |
| `SYNAPSE_WRITE_BATCH`     | `on`           | `off` отключает групповой коммит записи  |
| `SYNAPSE_REGION`          | `local`        | метка региона (хук мультирегиона)        |

Любое подмножество можно задать; незаданные бэкенды падают обратно на режим «в памяти».

## Тесты

```bash
go test ./...                                        # юнит + end-to-end
make ci                                              # vet + build + race-тесты + govulncheck + gosec
go test ./pkg/wire -fuzz=FuzzParser -fuzztime=30s    # фаззинг парсера протокола
```

`.github/workflows/ci.yml` на каждый push/PR гоняет race-набор тестов плюс
**govulncheck** (скан CVE в зависимостях и стандартной библиотеке), **gosec**
(статический анализ безопасности) и короткий фаззинг парсера.

`internal/gateway/integration_test.go` прогоняет реальных клиентов через шлюз и
проверяет доставку, порядок, идемпотентность, **кросс-узловую доставку** (два узла),
**replay при resume**, экспорт чата, доставку источника пересылки и срока
самоуничтожения до клиента, а также **полный обмен по Double Ratchet**.
`internal/rpc/message_test.go` гоняет тот же путь записи и чтения через настоящий
gRPC-хоп, а `internal/gateway/fleet_integration_test.go` поднимает шлюз, у которого
**все** сервисы удалённые, — чтобы разделённый деплой не терял поля, которые
монолит доставляет.
Тесты, которым нужна инфраструктура, пропускаются без env-DSN
(`SYNAPSE_TEST_PG_DSN`, `SYNAPSE_TEST_REDIS_ADDR`, `SYNAPSE_TEST_NATS_URL`).

**Шардированное хранилище сообщений.** `SYNAPSE_TEST_SHARD_DSNS` (два и более DSN
через запятую) прогоняет `internal/store/sharded` и `internal/platform` на
настоящих шардах: co-location и беспробельный per-chat seq на каждом бэкенде,
свой outbox у каждого шарда и способности, которые ходят по всем шардам, —
репер самоуничтожения и проверка ссылок на медиа — доходящие до данных, которые
хеш положил в шард, никем не названный.

Каждому DSN нужна **своя база**, в том числе отдельно от `SYNAPSE_TEST_PG_DSN`:
outbox — общая таблица, которую оба набора тестов вычищают, поэтому два DSN на
одну базу означают, что тесты удаляют друг у друга застейдженные события:

```bash
SYNAPSE_TEST_PG_DSN="postgres://synapse:synapse@localhost:5432/synapse_primary?sslmode=disable" SYNAPSE_TEST_SHARD_DSNS="postgres://synapse:synapse@localhost:5433/synapse?sslmode=disable,postgres://synapse:synapse@localhost:5434/synapse?sslmode=disable"   go test ./...
```

### Нагрузочный тест

```bash
SYNAPSE_SEND_RATE=100000 go run ./cmd/server
go run ./cmd/loadtest -addr localhost:7000 -conns 200 -msgs 50   # режим throughput

# режим idle-scale: держать N соединений и мерить стоимость на соединение
SYNAPSE_PPROF=1 go run ./cmd/server
go run ./cmd/loadtest -addr localhost:7000 -conns 5000 -idle 30s
```

Режим throughput выдаёт перцентили латентности send→ack; режим idle-scale — число
горутин и удержанную память (с форс-GC) **на соединение** (замер: ~2 горутины и
~24 КиБ/conn → проекция ~2.3 ГиБ на 100k соединений на одном узле).

## Структура

```
pkg/wire        протокол (кадр, конверт, protobuf-кодек, zstd, TCP/WS/QUIC)
pkg/id          генератор Snowflake-ID
pkg/eventbus    шина событий (в памяти + NATS JetStream)
pkg/e2e         сквозная криптография (X3DH + Double Ratchet, подписи предключей)
pkg/ratelimit · breaker · mtls   флуд-контроль, circuit breaker, mTLS-хелпер
proto/          protobuf-схемы → сгенерированный internal/wirepb
internal/store  контракты + memory и postgres (групповой коммит, миграции)
internal/auth   идентичность/сессии (argon2id, хеш токенов)
internal/chat · message(+брокер) · fanout · presence   доменные сервисы
internal/router · keydir  кросс-узловой реестр и каталог предключей (memory + Redis)
internal/media · search · moderation · notify · audit   сервисы V1
internal/reaction · poll · call   реакции, опросы с подсчётом, сигналинг звонков
internal/contact · pin · invite   контакты и блокировки, закрепления и черновики,
                                  публичные адреса, ссылки, роли
internal/schedule отложенная отправка (диспетчер) + репер самоуничтожения
internal/rpc · platform   gRPC-адаптеры и общий бутстрап демонов cmd/*d
internal/outbox · replay · nodeid   outbox-relay, resume-буфер, лиз node-id
internal/metrics · tracing   Prometheus + OpenTelemetry
internal/gateway realtime-шлюз (рукопожатие, авторизация, QoS, backpressure, reaper, QUIC)
cmd/server · cmd/client · cmd/loadtest
cmd/gatewayd · authd · chatd · messaged · presenced · keydird   gRPC-демоны
cmd/fanoutd · notifyd · moderationd · searchd   воркеры шины
deploy/observability  Prometheus + Tempo + Grafana
```

Полный дизайн, компромиссы, каталог сервисов, поток сообщений, матрица хранилищ,
реестр рисков и дорожная карта — в [ARCHITECTURE.md](ARCHITECTURE.md). Аудит
безопасности — в [SECURITY.md](SECURITY.md).
