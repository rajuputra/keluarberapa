# KeluarBerapa – Architecture & Technical Specification

## 1. Non-Negotiable Technical Decisions
*   **Architecture:** Multi-user from day one.
*   **Frontend:** Astro + TypeScript.
*   **Backend:** Go REST API.
*   **Database:** PostgreSQL.
*   **WhatsApp:** Meta WhatsApp Cloud API (Exactly 1 number per user).
*   **Parser:** Rule-based / Regex.
*   **Transaction confirmation:** Direct save.
*   **Infrastructure:** Synchronous processing (No Redis, No WebSocket, No Message Queue).
*   **Data Types:** UUID for primary IDs, BIGINT for money, IDR currency, Asia/Jakarta default timezone.

## 2. Authentication & Security Design
*   **Auth:** JWT access (15m) + refresh token (30d). Refresh tokens stored hashed in Postgres.
*   **Password:** Argon2id or bcrypt. Never stored in plaintext.
*   **Multi-Tenant Isolation (P0):** Every authenticated request gets `UserContext.UserID`. Repository queries MUST use that ID.
*   **WhatsApp Webhook:** Validate Meta webhook signature. Resolve user from `phone_number`. Duplicate message ID must not create duplicate transactions.

## 3. Project Folder Structure (Backend)
```text
backend/
├── cmd/api/main.go
├── internal/
│   ├── auth/ (handler, service, repository, model)
│   ├── user/
│   ├── whatsapp/ (handler, service, meta_client, parser, model)
│   ├── transaction/
│   ├── dashboard/
│   ├── category/
│   ├── middleware/ (auth, cors, logging, recovery)
│   ├── database/postgres.go
│   ├── config/config.go
│   └── http/router.go
├── migrations/001_initial_schema.sql
├── docs/openapi.yaml
```

## 4. Database Schema (PostgreSQL)
*   `users`: id, name, email, password_hash, status, timezone.
*   `whatsapp_accounts`: id, user_id, phone_number, provider, verification_status.
*   `categories`: id, user_id, name, is_system.
*   `transactions`: id, user_id, category_id, amount, currency, description, transaction_date, source, whatsapp_message_id.
*   `whatsapp_messages`: Stores inbound messages for idempotency.
*   `refresh_tokens`: Stores hashed tokens.

## 5. API & Environment
*   Standard API Error Format: `{"error": "...", "message": "..."}`
*   Required ENV Vars: `DATABASE_URL`, `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET`, `WHATSAPP_VERIFY_TOKEN`, `WHATSAPP_ACCESS_TOKEN`, etc.