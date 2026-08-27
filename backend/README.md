# KeluarBerapa Backend

Go REST API for KeluarBerapa, a multi-user expense tracker where expenses are
recorded by sending a WhatsApp message such as `Makan 25000` to a single central
bot, and reviewed on a web dashboard.

The authoritative specifications live one directory up: [`../prd.md`](../prd.md),
[`../architecture.md`](../architecture.md), [`../user_stories.md`](../user_stories.md)
and [`../ai_instructions.md`](../ai_instructions.md).

## Stage status

**Stage 1 — Database and backend foundation: complete.**

| Delivered | Not yet implemented |
| --- | --- |
| Go module, folder layout, build | Registration, login, JWT issuing |
| Environment configuration with validation | WhatsApp webhook, Meta client, expense parser |
| PostgreSQL connection pool | Dashboard summary and transaction endpoints |
| `001_initial_schema.sql` and an embedded migration runner | |
| Domain models for all six tables | |
| `GET /health` and `GET /ready` | |
| Graceful shutdown, structured logging, CORS, panic recovery | |
| Unit tests plus database integration tests | |

Later stages add their endpoints to [`docs/openapi.yaml`](docs/openapi.yaml) in
the same change that implements them.

## Requirements

- **Go 1.26+**
- **PostgreSQL 13+** — the schema uses `gen_random_uuid()`, a core function since
  PostgreSQL 13, so no extension and no superuser privilege is needed.

## Quick start

```bash
cd backend

# 1. Configuration. Real environment variables always win over .env.
cp .env.example .env
# Then edit DATABASE_URL in .env.

# 2. Dependencies.
go mod download

# 3. Schema.
go run ./cmd/migrate -action up

# 4. Run.
go run ./cmd/api
```

Verify it is up:

```bash
curl -s localhost:8080/health
# {"status":"ok","service":"keluarberapa-api","version":"0.1.0","env":"development", ...}

curl -s localhost:8080/ready
# {"status":"ready","checks":{"database":{"status":"ok","latency_ms":1}}}
```

`make help` lists shortcuts for all of the above.

### A PostgreSQL for local development

```bash
docker run -d --name keluarberapa-db \
  -e POSTGRES_USER=keluarberapa \
  -e POSTGRES_PASSWORD=change-me \
  -e POSTGRES_DB=keluarberapa \
  -p 5432:5432 postgres:16-alpine
```

Matching DSN:

```
DATABASE_URL=postgres://keluarberapa:change-me@localhost:5432/keluarberapa?sslmode=disable
```

## Endpoints

Stage 1 exposes only the operational probes. They are unauthenticated, sit
outside `/api/v1`, and return no user data.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Liveness. Touches no dependency, so a slow database cannot get a healthy process restarted. |
| `GET` | `/ready` | Readiness. Pings PostgreSQL within `DATABASE_HEALTH_TIMEOUT`; `503` when unavailable. |

Every error, including a 404, uses the single envelope from `architecture.md`
section 5:

```json
{ "error": "not_found", "message": "The requested resource was not found." }
```

A `/ready` failure keeps that envelope and adds a `checks` object so an operator
can see which dependency failed. The body carries no driver detail — a
connection error can name internal hosts — and the full error goes to the log
under the same `X-Request-Id`.

Full contract: [`docs/openapi.yaml`](docs/openapi.yaml).

## Environment variables

Every variable, its default and whether it is required is documented inline in
[`.env.example`](.env.example). Summary:

| Variable | Required | Notes |
| --- | --- | --- |
| `DATABASE_URL` | always | `postgres://` or `postgresql://`. PostgreSQL 13+. |
| `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET` | production | 32+ characters, must differ. |
| `WHATSAPP_VERIFY_TOKEN`, `WHATSAPP_ACCESS_TOKEN`, `WHATSAPP_APP_SECRET`, `WHATSAPP_PHONE_NUMBER_ID` | production | Consumed by the WhatsApp stage. |
| `APP_ENV`, `PORT`, `LOG_LEVEL`, `LOG_FORMAT`, `APP_TIMEZONE`, `CORS_ALLOWED_ORIGINS`, `DATABASE_*`, `HTTP_*`, `JWT_*` | optional | Defaults in `.env.example`. |

Notes on the design:

- **Secrets never render.** Secret values use a `config.Secret` type whose
  `String`, `GoString`, `LogValue`, `MarshalJSON` and `MarshalText` all return
  `[REDACTED]`. Only an explicit `Reveal()` at the point of use returns the real
  value, so a secret cannot reach a log line or a response by accident.
- **All problems reported at once.** A misconfigured deployment gets every
  invalid variable in a single error rather than one per restart.
- **Production is stricter.** Auth and WhatsApp credentials are optional locally
  (Stage 1 needs none of them) and mandatory when `APP_ENV=production`, where a
  `*` CORS origin is also rejected.
- **`.env` never overrides the real environment**, and a missing `.env` is not an
  error. Set `ENV_FILE` to load a different file.

`.env` is gitignored. Do not commit secrets.

## Database migrations

Migrations are plain SQL in [`migrations/`](migrations), named
`<version>_<snake_case_name>.sql`, and are embedded into both binaries with
`go:embed`. No migration CLI needs to be installed.

```bash
go run ./cmd/migrate -action up       # apply everything pending
go run ./cmd/migrate -action status   # show applied and pending
```

Behaviour worth knowing:

- Each file runs **inside its own transaction** together with its
  `schema_migrations` row, so a failure leaves the previous version intact
  rather than a half-migrated schema. Migration files must therefore not contain
  `BEGIN`/`COMMIT` themselves.
- A **session advisory lock** serialises runs, so two API instances starting
  together cannot apply the same file twice.
- The **SHA-256 of each file is recorded**. Editing a migration that has already
  run is refused rather than silently skipped — add a new migration instead.
- Setting `DATABASE_AUTO_MIGRATE=true` applies pending migrations on API
  startup. Leave it off where migrations are a separate deploy step.

### Adding a migration

1. Create `migrations/002_<name>.sql`. Never edit an applied file.
2. Write forward-only SQL, no transaction control.
3. `go run ./cmd/migrate -action up`, then `-action status` to confirm.

## Schema notes and documented additions

`architecture.md` section 4 names the tables and their core columns. Types,
constraints and indexes are derived from section 1 (UUID keys, `BIGINT` money,
IDR, `Asia/Jakarta`), section 2 (isolation, hashed refresh tokens) and the
acceptance criteria in `user_stories.md`. Per `ai_instructions.md` section 1.2,
everything added beyond the named columns is listed here.

**Added to every table:** `created_at`, and `updated_at` on mutable tables, kept
current by a `set_updated_at()` trigger so hand-written SQL cannot leave it
stale.

**Per table:**

| Table | Addition | Why |
| --- | --- | --- |
| `users` | unique index on `lower(email)` | `user_stories.md` Epic 1 requires email uniqueness; case-insensitive so `A@b.com` cannot create a second account. |
| `users` | `status` and blank-name/email checks | Mirrors the model's `Status` values. |
| `whatsapp_accounts` | `UNIQUE (user_id)` | PRD section 1.3: exactly one number per user, enforced by the database rather than by application code alone. |
| `whatsapp_accounts` | `UNIQUE (phone_number)` | Inbound identity resolution must be unambiguous (`ai_instructions.md` section 3). |
| `whatsapp_accounts` | `verified_at` + consistency check | The verification flow needs a timestamp; the check stops `verified_at` and `verification_status` from disagreeing. |
| `categories` | `user_id` nullable, ownership check | The six PRD defaults are seeded as shared system rows (`user_id IS NULL`, `is_system = TRUE`); user-created categories always carry a `user_id`. The check makes "whose category is this?" unambiguous. |
| `whatsapp_messages` | `provider_message_id`, `from_phone_number`, `body`, `status`, `error_reason`, `received_at`, `processed_at` | `architecture.md` only says "stores inbound messages for idempotency"; these are the columns that requires. `UNIQUE (provider, provider_message_id)` is the idempotency key. |
| `whatsapp_messages` | `user_id` nullable | A message from an unregistered number must still be recorded, or a retry would look new every time — but it has no owning user. |
| `transactions` | `deleted_at` | `user_stories.md` Epic 3 requires listings to exclude deleted transactions rather than lose them. |
| `transactions` | unique partial index on `whatsapp_message_id` | Second line of defence for idempotency: one message can never produce two transactions, even if application logic is wrong. |
| `transactions` | amount/currency/source/description checks | Money must be positive, currency is IDR only, and a `whatsapp` transaction must reference its message while a `web` one must not. |
| `transactions` | indexes on `(user_id, transaction_date DESC)` and `(user_id, category_id)` | Dashboard queries. `user_id` leads both because every query is scoped to the authenticated user. |
| `refresh_tokens` | `revoked_at`, unique `token_hash` | Logout needs revocation; lookup is by hash, so the hash must be unique. |

**Type choices not fixed by the specification:**

- `transaction_date` is `TIMESTAMPTZ`, not `DATE`. Time of day is needed to order
  a day's expenses in the transaction list; monthly buckets come from
  `transaction_date AT TIME ZONE users.timezone`.
- `amount` is `BIGINT` in the smallest unit of the currency. IDR has no minor
  unit, so `25000` means Rp25.000. Money is never floating point.
- Enum-like columns are `TEXT` with `CHECK` constraints rather than PostgreSQL
  `ENUM` types, because adding a value later is a plain constraint change
  instead of a type migration.
- Sessions run with `timezone=UTC`, so stored timestamps never depend on the
  server's locale.

**Open question for the product owner** (`ai_instructions.md` section 1.11): the
specifications say a WhatsApp number is registered and then verified, but not
*how*. No verification-code columns were invented; the mechanism needs deciding
before the WhatsApp identity stage, and it will need its own migration.

## Multi-tenant isolation

Data leakage between users is an absolute failure condition
(`ai_instructions.md` section 3). Stage 1 puts the foundations in place:

- Every tenant-owned row carries `user_id`, and `transactions.user_id` is
  `NOT NULL` — a transaction with no owner would be invisible to every scoped
  query and belong to nobody.
- `whatsapp_accounts` guarantees one number per user *and* one user per number,
  so `phone_number → user_id` resolution cannot be ambiguous.
- All SQL binds parameters (`$1`, `$2`, …). No query is built by string
  concatenation.
- 404 and "not yours" are indistinguishable: the not-found message never reveals
  whether a resource exists.
- Every claim above is asserted by a test in
  [`internal/database/schema_test.go`](internal/database/schema_test.go),
  including that a `user_id`-scoped `SELECT`, `UPDATE` and soft `DELETE` all miss
  another user's rows, and that deleting a user removes only their data.

When adding a repository in a later stage: take the user id from the JWT context
only, never from a request body or query string, and put `user_id = $1` in every
statement — including `UPDATE` and `DELETE` by id.

## Project layout

```
backend/
├── cmd/
│   ├── api/main.go              # API entry point: config, pool, router, shutdown
│   └── migrate/main.go          # migration CLI (-action up|status)
├── internal/
│   ├── auth/model.go            # RefreshToken
│   ├── category/model.go        # Category + the PRD's system category names
│   ├── config/                  # env loading, validation, Secret, .env parser
│   ├── database/                # pool (postgres.go) and migrator (migrate.go)
│   ├── http/                    # router, response envelope, health, server
│   ├── logging/logger.go        # slog setup
│   ├── middleware/              # request id, request logging, recovery, CORS
│   ├── transaction/model.go     # Transaction
│   ├── user/model.go            # User
│   └── whatsapp/model.go        # Account, Message
├── migrations/001_initial_schema.sql
├── docs/openapi.yaml
├── .env.example
└── Makefile
```

Two deviations from the layout in `architecture.md` section 3, both deliberate:

- **`internal/logging/`** is not in that list. Structured logging is an explicit
  Stage 1 requirement and both binaries plus the tests need the same setup, so it
  is a package rather than code duplicated in `main`.
- **`internal/http` declares `package httpapi`.** The path is the one the
  specification asks for; the package name differs so files inside can import
  `net/http` without an alias on every reference.

Domain packages (`auth`, `user`, `whatsapp`, `transaction`, `category`) currently
hold only models. Their handler, service and repository files arrive with the
stage that needs them, matching the structure in `architecture.md` section 3.

## Running the tests

```bash
go test ./...          # unit tests; database tests skip themselves
gofmt -l .             # must print nothing
go vet ./...
```

Database integration tests run only when `TEST_DATABASE_URL` is set, so
`go test ./...` passes on a machine with no PostgreSQL:

```bash
export TEST_DATABASE_URL="postgres://keluarberapa:change-me@localhost:5432/keluarberapa_test?sslmode=disable"
go test ./... -count=1
```

Each test creates its own PostgreSQL **schema**, migrates it, and drops it
afterwards. That needs no `CREATE DATABASE` privilege and keeps tests isolated
from each other, but point `TEST_DATABASE_URL` at a throwaway database anyway.

A disposable database in one command:

```bash
docker run -d --name keluarberapa-test-db \
  -e POSTGRES_USER=keluarberapa \
  -e POSTGRES_PASSWORD=change-me \
  -e POSTGRES_DB=keluarberapa_test \
  -p 5432:5432 postgres:16-alpine
```

What the suite covers:

| Area | File |
| --- | --- |
| Config parsing, validation, production strictness | `internal/config/config_test.go` |
| Secret redaction across every rendering path | `internal/config/secret_test.go` |
| `.env` parsing and precedence | `internal/config/dotenv_test.go` |
| Migration loading, ordering, checksums | `internal/database/migrate_test.go` |
| Schema, constraints, **multi-user isolation** | `internal/database/schema_test.go` |
| `/health`, `/ready`, timeouts, no-leak assertions | `internal/http/health_test.go` |
| Routing, error envelope, 404/405, panic recovery | `internal/http/router_test.go` |
| Graceful shutdown and connection draining | `internal/http/server_test.go` |
| Request id, request logging, recovery, CORS | `internal/middleware/middleware_test.go` |
| Model invariants and ownership checks | `internal/*/model_test.go` |

## Operational behaviour

- **Graceful shutdown.** `SIGINT`/`SIGTERM` stops the listener and gives running
  handlers up to `HTTP_SHUTDOWN_TIMEOUT` to finish; the connection pool is closed
  only after the server has fully stopped. A second signal kills the process
  immediately instead of being swallowed. If the grace period elapses, remaining
  connections are closed and the timeout is reported rather than hanging.
- **Structured logging.** `log/slog`, JSON by default (`LOG_FORMAT=text` for
  local work). Every line carries `service`, `version` and `env`; every request
  line carries `request_id`. Request and response **bodies are never logged** —
  they can hold passwords and WhatsApp message text. `/health` and `/ready` log
  at debug so probes do not drown out real traffic.
- **Request correlation.** A well-formed inbound `X-Request-Id` (8–64 characters
  of `A-Za-z0-9._:-`) is reused so a trace survives across services; anything
  else is replaced, because that value reaches log lines and response headers.
- **Panic recovery.** A panic becomes a logged `500` in the standard error
  envelope. The panic value and stack trace go to the log only. If the handler
  had already started writing, the response is left alone rather than corrupted.
- **CORS.** Origins are matched exactly against `CORS_ALLOWED_ORIGINS`. The
  concrete origin is echoed rather than `*`, so the header stays valid if
  credentialed requests are enabled later.

## Contributing

Follow `ai_instructions.md`. In particular:

1. No Redis, message queue, WebSocket, OAuth or microservices.
2. Expense parsing stays rule-based/regex; no LLM.
3. Take the user id from the JWT context only; never trust a client-supplied one.
4. Never log or return a password, a password hash, or a token.
5. Write tests with the implementation, and run `gofmt`, `go vet` and `go test`
   before committing (`make check`).
6. Do not change `docs/openapi.yaml` silently — update it in the same change as
   the code.
