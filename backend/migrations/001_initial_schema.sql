-- =============================================================================
-- KeluarBerapa - 001_initial_schema
-- =============================================================================
-- Source of truth: architecture.md section 4 (Database Schema). Every table and
-- every column named there exists below; additions are limited to audit
-- timestamps, the constraints implied by the specification, and the columns
-- required by the acceptance criteria in user_stories.md. All additions are
-- listed in backend/README.md ("Schema notes and documented additions").
--
-- Conventions (architecture.md section 1 - Data Types):
--   * Primary keys are UUID. gen_random_uuid() is a core function since
--     PostgreSQL 13, so no extension is required.
--   * Money is BIGINT in the smallest unit of the currency. IDR has no minor
--     unit, so 25000 means Rp25.000. Floating point is never used for money.
--   * Timestamps are TIMESTAMPTZ (stored UTC). Presentation is converted to the
--     user's timezone, defaulting to Asia/Jakarta.
--   * Every tenant-owned row carries user_id so repository queries can always
--     filter on it (architecture.md section 2 - Multi-Tenant Isolation, P0).
--
-- This file is executed inside a single transaction by the migrator
-- (internal/database/migrate.go), therefore it must not contain BEGIN/COMMIT.
-- =============================================================================


-- -----------------------------------------------------------------------------
-- Shared trigger function: keeps updated_at honest even for hand-written SQL.
-- -----------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;


-- -----------------------------------------------------------------------------
-- users
-- architecture.md: id, name, email, password_hash, status, timezone
-- -----------------------------------------------------------------------------
CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT        NOT NULL,
    email         TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'active',
    timezone      TEXT        NOT NULL DEFAULT 'Asia/Jakarta',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT users_name_not_blank  CHECK (btrim(name) <> ''),
    CONSTRAINT users_email_not_blank CHECK (btrim(email) <> ''),
    CONSTRAINT users_status_valid    CHECK (status IN ('active', 'inactive', 'suspended'))
);

-- Email uniqueness is case-insensitive (user_stories.md Epic 1: "Validates
-- email uniqueness"). Registration must normalise to lower case before lookup.
CREATE UNIQUE INDEX users_email_unique ON users (lower(email));

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE  users            IS 'Application accounts. password_hash is Argon2id/bcrypt and is never returned by the API.';
COMMENT ON COLUMN users.timezone   IS 'IANA timezone used to render dates and to bucket monthly summaries. Defaults to Asia/Jakarta.';


-- -----------------------------------------------------------------------------
-- whatsapp_accounts
-- architecture.md: id, user_id, phone_number, provider, verification_status
-- Rule (PRD section 1.3 / architecture.md section 1): exactly one number per user.
-- -----------------------------------------------------------------------------
CREATE TABLE whatsapp_accounts (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    phone_number        TEXT        NOT NULL,
    provider            TEXT        NOT NULL DEFAULT 'meta_cloud_api',
    verification_status TEXT        NOT NULL DEFAULT 'pending',
    verified_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- "Exactly 1 WhatsApp number per user" is enforced by the database, not by
    -- application code alone.
    CONSTRAINT whatsapp_accounts_one_per_user UNIQUE (user_id),
    -- A phone number resolves to exactly one user. Without this, inbound
    -- webhook identity resolution would be ambiguous, which ai_instructions.md
    -- section 3 classifies as an absolute failure condition.
    CONSTRAINT whatsapp_accounts_phone_unique UNIQUE (phone_number),
    CONSTRAINT whatsapp_accounts_phone_format CHECK (phone_number ~ '^[1-9][0-9]{6,14}$'),
    CONSTRAINT whatsapp_accounts_provider_valid CHECK (provider IN ('meta_cloud_api')),
    CONSTRAINT whatsapp_accounts_verification_valid
        CHECK (verification_status IN ('pending', 'verified', 'failed')),
    -- verified_at and the status cannot disagree.
    CONSTRAINT whatsapp_accounts_verified_at_consistent CHECK (
        (verification_status = 'verified' AND verified_at IS NOT NULL) OR
        (verification_status <> 'verified' AND verified_at IS NULL)
    )
);

CREATE TRIGGER whatsapp_accounts_set_updated_at
    BEFORE UPDATE ON whatsapp_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE  whatsapp_accounts              IS 'Maps a WhatsApp number to its owning user. One row per user (MVP).';
COMMENT ON COLUMN whatsapp_accounts.phone_number IS 'E.164 digits without the leading + (Meta wa_id format), e.g. 6281234567890.';


-- -----------------------------------------------------------------------------
-- categories
-- architecture.md: id, user_id, name, is_system
-- PRD section 5 default categories are seeded as system rows (user_id IS NULL)
-- and are read-only reference data shared by every tenant. User-created
-- categories (post-MVP P1) always carry user_id.
-- -----------------------------------------------------------------------------
CREATE TABLE categories (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    is_system  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT categories_name_not_blank CHECK (btrim(name) <> ''),
    -- A system category is global and unowned; a user category must be owned.
    -- This makes "whose category is this?" unambiguous.
    CONSTRAINT categories_ownership_valid CHECK (
        (is_system AND user_id IS NULL) OR (NOT is_system AND user_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX categories_system_name_unique
    ON categories (lower(name)) WHERE user_id IS NULL;
CREATE UNIQUE INDEX categories_user_name_unique
    ON categories (user_id, lower(name)) WHERE user_id IS NOT NULL;

CREATE TRIGGER categories_set_updated_at
    BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE  categories         IS 'Expense categories. user_id IS NULL means a shared system category.';
COMMENT ON COLUMN categories.user_id IS 'Owning user, or NULL for the seeded system categories.';

-- PRD section 5: Makan, Transportasi, Hiburan, Tagihan, Belanja, Lainnya.
-- 'Lainnya' is the fallback when no keyword rule matches.
INSERT INTO categories (name, is_system) VALUES
    ('Makan',        TRUE),
    ('Transportasi', TRUE),
    ('Hiburan',      TRUE),
    ('Tagihan',      TRUE),
    ('Belanja',      TRUE),
    ('Lainnya',      TRUE);


-- -----------------------------------------------------------------------------
-- whatsapp_messages
-- architecture.md: "Stores inbound messages for idempotency".
-- Created before transactions because transactions references it.
-- -----------------------------------------------------------------------------
CREATE TABLE whatsapp_messages (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Nullable on purpose: messages from unregistered numbers must still be
    -- recorded so a retried webhook is not processed twice
    -- (user_stories.md Epic 2), but they have no owning user.
    user_id             UUID        REFERENCES users (id) ON DELETE SET NULL,
    provider            TEXT        NOT NULL DEFAULT 'meta_cloud_api',
    provider_message_id TEXT        NOT NULL,
    from_phone_number   TEXT        NOT NULL,
    body                TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'received',
    error_reason        TEXT,
    received_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The idempotency key. A duplicate webhook delivery hits this constraint
    -- and must be answered with 200 without creating a second transaction.
    CONSTRAINT whatsapp_messages_provider_message_unique
        UNIQUE (provider, provider_message_id),
    CONSTRAINT whatsapp_messages_provider_valid CHECK (provider IN ('meta_cloud_api')),
    CONSTRAINT whatsapp_messages_status_valid
        CHECK (status IN ('received', 'processed', 'rejected', 'ignored'))
);

CREATE INDEX whatsapp_messages_user_received_idx
    ON whatsapp_messages (user_id, received_at DESC);
CREATE INDEX whatsapp_messages_from_phone_idx
    ON whatsapp_messages (from_phone_number, received_at DESC);

COMMENT ON TABLE  whatsapp_messages                     IS 'Inbound WhatsApp messages. Exists for idempotency and troubleshooting.';
COMMENT ON COLUMN whatsapp_messages.provider_message_id IS 'Provider message id (Meta wamid). Unique per provider.';
COMMENT ON COLUMN whatsapp_messages.status              IS 'received -> processed (transaction saved) | rejected (unparsable) | ignored (unknown sender or non-text).';


-- -----------------------------------------------------------------------------
-- transactions
-- architecture.md: id, user_id, category_id, amount, currency, description,
--                  transaction_date, source, whatsapp_message_id
-- -----------------------------------------------------------------------------
CREATE TABLE transactions (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- NOT NULL: ai_instructions.md section 1.6 - every transaction must have a user_id.
    user_id             UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category_id         UUID        REFERENCES categories (id) ON DELETE SET NULL,
    amount              BIGINT      NOT NULL,
    currency            TEXT        NOT NULL DEFAULT 'IDR',
    description         TEXT        NOT NULL,
    transaction_date    TIMESTAMPTZ NOT NULL DEFAULT now(),
    source              TEXT        NOT NULL,
    whatsapp_message_id UUID        REFERENCES whatsapp_messages (id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Soft delete: user_stories.md Epic 3 requires the transaction list to
    -- "exclude deleted transactions" rather than lose them.
    deleted_at          TIMESTAMPTZ,

    CONSTRAINT transactions_amount_positive     CHECK (amount > 0),
    CONSTRAINT transactions_currency_valid      CHECK (currency = 'IDR'),
    CONSTRAINT transactions_description_not_blank CHECK (btrim(description) <> ''),
    CONSTRAINT transactions_source_valid        CHECK (source IN ('whatsapp', 'web')),
    -- A WhatsApp-sourced transaction must point at the message that produced it;
    -- a web transaction must not.
    CONSTRAINT transactions_source_message_consistent CHECK (
        (source = 'whatsapp' AND whatsapp_message_id IS NOT NULL) OR
        (source <> 'whatsapp' AND whatsapp_message_id IS NULL)
    )
);

-- Second line of defence for idempotency: one inbound message can never
-- produce two transactions, even if the application logic is wrong.
CREATE UNIQUE INDEX transactions_whatsapp_message_unique
    ON transactions (whatsapp_message_id) WHERE whatsapp_message_id IS NOT NULL;

-- Dashboard listing and monthly summary. user_id leads every index because
-- every query is scoped to the authenticated user.
CREATE INDEX transactions_user_date_idx
    ON transactions (user_id, transaction_date DESC) WHERE deleted_at IS NULL;
CREATE INDEX transactions_user_category_idx
    ON transactions (user_id, category_id) WHERE deleted_at IS NULL;

CREATE TRIGGER transactions_set_updated_at
    BEFORE UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE  transactions                  IS 'Expense records. Always filter by user_id; never trust a client-supplied user id.';
COMMENT ON COLUMN transactions.amount           IS 'Positive integer in the smallest currency unit. IDR has none, so 25000 = Rp25.000.';
COMMENT ON COLUMN transactions.transaction_date IS 'When the expense happened (UTC). Bucket monthly summaries with AT TIME ZONE users.timezone.';
COMMENT ON COLUMN transactions.deleted_at       IS 'Soft delete marker. Queries must add "deleted_at IS NULL".';


-- -----------------------------------------------------------------------------
-- refresh_tokens
-- architecture.md section 2: "Refresh tokens stored hashed in Postgres" (30d).
-- -----------------------------------------------------------------------------
CREATE TABLE refresh_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Hash only. The raw refresh token never touches the database.
    token_hash TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT refresh_tokens_hash_not_blank CHECK (btrim(token_hash) <> ''),
    CONSTRAINT refresh_tokens_revoked_after_creation CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE UNIQUE INDEX refresh_tokens_token_hash_unique ON refresh_tokens (token_hash);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id, expires_at DESC);

COMMENT ON TABLE  refresh_tokens            IS 'Hashed refresh tokens. Lookup is by hash; the plaintext token is only ever held by the client.';
COMMENT ON COLUMN refresh_tokens.token_hash IS 'SHA-256 (hex) of the refresh token. Never store the token itself.';
