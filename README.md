# KeluarBerapa

Multi-user expense tracking. You record an expense by sending a WhatsApp message
to a single central bot:

```
You:  Makan 25000
Bot:  ✅ Makan Rp25.000
```

and review your spending on a web dashboard. One WhatsApp bot serves every user;
the backend identifies you from the number you registered.

## Specifications

These documents are the source of truth. Code follows them, not the other way
around.

| Document | Contents |
| --- | --- |
| [`prd.md`](prd.md) | Product requirements: flows, message format, amount parsing, categories, phases |
| [`architecture.md`](architecture.md) | Non-negotiable technical decisions, auth design, folder layout, database schema |
| [`user_stories.md`](user_stories.md) | User stories with acceptance criteria |
| [`design_guidelines.md`](design_guidelines.md) | Frontend visual direction, screens, UI rules |
| [`ai_instructions.md`](ai_instructions.md) | Execution rules, definition of done, security boundaries |

## Stack

| Layer | Choice |
| --- | --- |
| Frontend | Astro + TypeScript (not started) |
| Backend | Go REST API — see [`backend/`](backend) |
| Database | PostgreSQL 13+ |
| WhatsApp | Meta WhatsApp Cloud API, one number per user |
| Expense parsing | Rule-based / regex, no LLM |

Deliberately absent: Redis, message queues, WebSockets, OAuth, microservices.
Processing is synchronous.

## Repository layout

```
keluarberapa/
├── backend/            Go REST API (see backend/README.md)
├── prd.md              product requirements
├── architecture.md     technical specification
├── user_stories.md     acceptance criteria
├── design_guidelines.md
└── ai_instructions.md  rules for anyone (or anything) writing code here
```

The Astro frontend will be added as a sibling directory to `backend/`.

## Progress

Phases are defined in `prd.md` section 6.

| Stage | Scope | Status |
| --- | --- | --- |
| 1 | Database and backend foundation | **Complete** |
| 2 | Authentication + user / WhatsApp identity | Not started |
| 3 | WhatsApp input, regex parser, transaction save | Not started |
| 4 | Dashboard monthly summary and transaction history | Not started |
| 5 | Security / integration testing, deployment | Not started |

Stage 1 delivered the Go module and layout, environment configuration, the
PostgreSQL connection and `001_initial_schema.sql`, models for all six tables,
`/health` and `/ready`, graceful shutdown, structured logging, and the test
suite — including tests that assert user data cannot cross tenant boundaries.

## Getting started

```bash
git clone https://github.com/rajuputra/keluarberapa.git
cd keluarberapa/backend
cp .env.example .env          # then set DATABASE_URL
go mod download
go run ./cmd/migrate -action up
go run ./cmd/api
```

```bash
curl -s localhost:8080/health
curl -s localhost:8080/ready
```

Full instructions, environment variables, schema notes and test setup:
[`backend/README.md`](backend/README.md).

## Ground rules

From [`ai_instructions.md`](ai_instructions.md):

- Multi-user isolation is a P0 invariant. Every transaction has a `user_id`, and
  every query filters on it. Data leaking between users is an absolute failure
  condition.
- The user id comes from the JWT context only. A `user_id` supplied by the
  frontend is never trusted.
- Passwords are hashed (Argon2id/bcrypt) and never returned. Refresh tokens are
  stored hashed. No secret is ever committed or logged.
- Architecture changes need explicit approval; ambiguous requirements get a
  question, not a guess.
- Tests are written with the implementation and run at the end of every stage.
