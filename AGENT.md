# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

KeluarBerapa: multi-user expense tracking. A user texts `Makan 25000` to one
central WhatsApp bot; the backend resolves the user from the sender's number,
parses the message with regex, saves the transaction, and replies. A web
dashboard shows monthly summaries.

**This is a spec-driven repository.** Five markdown files at the root are the
contract, not documentation-after-the-fact:

| File                   | Role                                                        |
| ---------------------- | ----------------------------------------------------------- |
| `ai_instructions.md`   | Hard rules + definition of done. Read this first.           |
| `architecture.md`      | Non-negotiable tech decisions, folder layout, DB schema     |
| `prd.md`               | Product requirements, message format, amount/category rules |
| `user_stories.md`      | Acceptance criteria                                         |
| `design_guidelines.md` | Frontend direction (frontend not started)                   |

Rules from `ai_instructions.md` that constrain almost every change:

- **No Redis, message queue, WebSocket, OAuth, or microservices.** Processing is
  synchronous.
- **No LLM for expense parsing** — regex/rules only.
- **Never trust a client-supplied `user_id`.** It comes from the JWT context.
- Do not invent API endpoints or DB fields without explaining why; do not change
  architecture without approval; if a requirement is ambiguous, ask instead of
  deciding a product question.
- Do not change `backend/docs/openapi.yaml` silently — update it in the same
  change as the code.

## Stage discipline

Work is staged (`prd.md` §6). **Only Stage 1 (database + backend foundation) is
implemented.** Authentication, the WhatsApp webhook/parser, and the dashboard
endpoints are not.

Consequently the domain packages (`internal/auth`, `user`, `whatsapp`,
`transaction`, `category`) contain **only `model.go`**. Their `handler.go`,
`service.go` and `repository.go` arrive with the stage that needs them, in the
shape `architecture.md` §3 describes. Don't scaffold them early.

`backend/README.md` tracks stage status and lists every documented deviation
from the spec.

## Commands

All Go work happens in `backend/`. A `Makefile` wraps these, but prefer the raw
`go` commands — `make` on this machine is GNU Make 3.81 via GnuWin32 and its
shell recipes are not reliable on Windows.

```bash
cd backend

go build ./...                                    # compile
go run ./cmd/api                                  # run the API (reads .env)
go run ./cmd/migrate -action up                   # apply pending migrations
go run ./cmd/migrate -action status               # show applied vs pending

gofmt -l .                                        # must print nothing
gofmt -w .
go vet ./...
go test ./...
```

Run those three checks (`gofmt -l .`, `go vet ./...`, `go test ./...`) before
reporting any change complete.

### Single test

```bash
go test ./internal/config/ -run TestLoadDefaults -v
go test ./internal/database/ -run 'TestTransactionCheckConstraints/zero_amount' -v
go test ./internal/http/ -run 'TestServe' -v
```

`-race` requires `CGO_ENABLED=1`, which is not configured here; it fails with
"`-race` requires cgo" unless a C toolchain is set up.

### Database tests

Database tests **skip themselves** unless `TEST_DATABASE_URL` is set, so
`go test ./...` passes with no PostgreSQL present. To actually exercise the
schema:

```bash
docker run -d --name keluarberapa-test-db \
  -e POSTGRES_USER=keluarberapa -e POSTGRES_PASSWORD=testpass \
  -e POSTGRES_DB=keluarberapa_test -p 55432:5432 postgres:16-alpine

TEST_DATABASE_URL="postgres://keluarberapa:testpass@localhost:55432/keluarberapa_test?sslmode=disable" \
  go test ./... -count=1
```

Each test creates its own throwaway PostgreSQL **schema** (not database) by
appending `search_path=<random>` to the DSN, migrates it, and drops it in
cleanup — see `internal/database/testsupport_test.go`. No `CREATE DATABASE`
privilege needed, and tests are isolated from each other.

PostgreSQL **13+** is required: the schema uses the core `gen_random_uuid()`.

## Architecture

### Layout

`backend/` is the Go module (`github.com/rajuputra/keluarberapa/backend`). The
Astro frontend will be a sibling directory. Dependencies are deliberately
minimal: `pgx/v5` and `google/uuid`. Routing is stdlib `net/http.ServeMux`
(method patterns), logging is `log/slog`, middleware is hand-rolled.

Two deliberate deviations from `architecture.md` §3, both explained in
`backend/README.md`:

- `internal/logging/` is not in the spec's list. Both binaries and the tests
  need the same slog setup.
- **`internal/http` declares `package httpapi`.** The directory matches the spec;
  the package name differs so files inside can import `net/http` without an
  alias. Import it as `httpapi "…/internal/http"`.

### Import direction: middleware must not import httpapi

`internal/http` (httpapi) imports `internal/middleware`, never the reverse.
`middleware.Recovery` therefore takes an injected `PanicHandler` callback rather
than reaching for the error-envelope writer; `httpapi.panicResponse` supplies
it. If new middleware needs to write a response body, inject a writer the same
way instead of adding an import.

### Configuration

`internal/config` is the **only** place that reads the environment. Nothing else
calls `os.Getenv`.

- **`config.Secret`** wraps sensitive values. `String`, `GoString`, `LogValue`,
  `MarshalJSON` and `MarshalText` all return `[REDACTED]`; only `Reveal()`
  returns the real value, and it should be called at the point of use (opening a
  connection, signing a token). Use it for anything credential-shaped.
- Validation **collects all errors** and returns them joined, so a bad deploy
  reports every problem in one pass. Add new variables via the `parser` helpers
  (`str`, `enum`, `duration`, `intRange`, `boolean`, `list`, `secret`).
- `APP_ENV=production` is stricter: JWT and WhatsApp credentials become
  mandatory and `*` CORS origins are rejected. Stage 1 needs none of them
  locally.
- `config.go` blank-imports `time/tzdata` so `Asia/Jakarta` resolves on Windows
  and scratch containers, which have no system zoneinfo. Don't remove it.
- `.env` is a convenience only: real environment variables always win, and a
  missing file is not an error. `ENV_FILE` overrides the path.

Every variable is documented inline in `backend/.env.example`.

### Migrations

Plain SQL in `backend/migrations/`, named `<version>_<snake_case_name>.sql`,
embedded via `migrations/embed.go` and run by `internal/database/migrate.go`.

Invariants to respect when adding migration `002_...`:

- **Never put `BEGIN`/`COMMIT` in a migration file.** The migrator wraps each
  file in a transaction together with its `schema_migrations` insert.
- **Never edit an applied migration.** The SHA-256 of each file is recorded, and
  the migrator hard-fails on a mismatch rather than skipping. Add a new file.
- A session advisory lock serialises runs, so concurrent API startups are safe.
- `DATABASE_AUTO_MIGRATE=true` applies migrations on API startup.

### Multi-tenant isolation (P0)

`ai_instructions.md` §3 makes cross-user data leakage an absolute failure
condition. The foundations, and what to preserve:

- Every tenant-owned row carries `user_id`; `transactions.user_id` is `NOT NULL`.
- `whatsapp_accounts` enforces **one number per user and one user per number** at
  the database level, so `phone_number → user_id` resolution cannot be ambiguous.
- All SQL binds parameters (`$1`, `$2`, …). Never build a query by concatenation.
- **404 and "not yours" must be indistinguishable.** `httpapi.notFound` returns
  the same message either way.
- When adding a repository: put `user_id = $1` in _every_ statement, including
  `UPDATE` and `DELETE` by id, and take the id from the JWT context only.
- `internal/database/schema_test.go` is where these claims are enforced — it
  asserts that scoped `SELECT`/`UPDATE`/soft-`DELETE` all miss another user's
  rows. Extend it when adding tenant-owned tables.

### Data conventions

- Money is `BIGINT` in the smallest currency unit. IDR has no minor unit, so
  `25000` means Rp25.000. **Never floating point.** Currency is IDR only
  (enforced by a `CHECK`).
- `transactions.transaction_date` is `TIMESTAMPTZ`, not `DATE` (time of day
  orders a day's expenses). Bucket monthly summaries with
  `transaction_date AT TIME ZONE users.timezone`; sessions run in UTC.
- Transactions are **soft-deleted** (`deleted_at`). Every listing needs
  `deleted_at IS NULL`.
- The six PRD default categories are seeded as **global system rows**
  (`user_id IS NULL`, `is_system = TRUE`). The scoped query is
  `WHERE user_id = $1 OR user_id IS NULL`.
- Enum-like columns are `TEXT` + `CHECK`, mirrored by Go types with a `Valid()`
  method in the domain package. Change both together.

### HTTP conventions

- The error envelope is **exactly** `{"error": "...", "message": "..."}`
  (`architecture.md` §5). `router_test.go` asserts the body has exactly two
  fields — adding one is a contract change requiring an `openapi.yaml` update.
  Error codes are constants in `internal/http/response.go`.
- `router.go` builds routes from a **`[]route` table**, then derives the
  path→methods map from it. This exists because the `/` catch-all (which makes
  unmatched requests return the JSON envelope instead of `net/http`'s plain-text 404) shadows ServeMux's built-in 405. Add routes to the table, not with a bare
  `mux.HandleFunc`.
- `/health` is liveness and **must never touch a dependency** — a slow database
  must not get a healthy process restarted. `/ready` does the dependency checks
  and returns 503. Its failure body carries no driver detail (a connection error
  can name internal hosts); the full error goes to the log under the same
  `X-Request-Id`.
- Middleware order in `NewRouter` is outermost-first: RequestID → RequestLogger →
  Recovery → CORS. Recovery sits _inside_ the logger so the 500 it writes gets
  logged.

### Logging

- Base attributes `service`, `version` and `env` are attached to every line by
  `logging.New`. **Do not reuse those keys** in a call site — that is why the
  migrator logs `schema_version`, not `version`.
- Request and response **bodies are never logged**; they can hold passwords and
  WhatsApp message text. `/health` and `/ready` log at debug so probes don't
  drown out traffic.
- An inbound `X-Request-Id` is reused only if it matches
  `^[A-Za-z0-9._:-]{8,64}$` — the value reaches log lines and response headers.

### Shutdown

`httpapi.Serve` owns the server lifecycle: it drains in-flight requests within
`HTTP_SHUTDOWN_TIMEOUT`, then closes what remains. `main` creates the listener
with `net.Listen` _before_ logging that it is up, so a taken port fails startup.
`db.Close()` is deferred in `main` and therefore runs only after `Serve` returns,
so no handler can be mid-query.

Windows has no `SIGTERM`, so shutdown behaviour is verified by testing
`httpapi.Serve` directly (`internal/http/server_test.go`) rather than by sending
signals.

## Open question

`prd.md` says a WhatsApp number is registered and then verified, but not _how_.
No verification-code columns were invented. The mechanism is a product decision
that must be settled before the WhatsApp identity stage, and it will need
migration `002`.
