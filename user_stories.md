# KeluarBerapa – User Stories & Acceptance Criteria

## Epic 1: Authentication & User Management
*   **User Story:** As a new user, I want to register using my email and password so I can have a secure account.
    *   *AC:* Validates email uniqueness. Hashes password with Argon2id/bcrypt. 
*   **User Story:** As a user, I want to log in and receive an access token so I can securely access the dashboard.
    *   *AC:* Returns JWT access and refresh tokens. Handles invalid credentials with standard 401 error.
*   **User Story:** As a user, I want to link my WhatsApp number so the bot can identify my messages.
    *   *AC:* Number is normalized. System enforces exactly 1 number per user.

## Epic 2: WhatsApp Webhook & Parsing
*   **User Story:** As a user, I want to text an expense (e.g., "Makan 25000") to the bot and have it recorded immediately.
    *   *AC:* System correctly parses amount and description. Defaults category if not matched. Saves transaction to DB. Returns success WA message.
*   **User Story:** As a user, if I type the wrong format, I want a helpful error message so I know how to fix it.
    *   *AC:* Rejects missing amounts or descriptions. Sends help template via WA.
*   **User Story:** As the system, I must ignore duplicate WhatsApp messages to prevent double-charging users.
    *   *AC:* Message ID is checked in `whatsapp_messages` table. Duplicate webhook triggers return 200 without creating a new transaction.
*   **User Story:** As the system, I must reject messages from unregistered phone numbers.
    *   *AC:* Checks `phone_number`. Sends registration prompt via WA. Does not save transaction.

## Epic 3: Dashboard & API
*   **User Story:** As a user, I want to see my total expenses and category breakdown for the current month.
    *   *AC:* GET `/api/v1/dashboard/monthly-summary` aggregates only the JWT user's data.
*   **User Story:** As a user, I want to see a paginated list of my transactions.
    *   *AC:* GET `/api/v1/transactions` returns paginated list. Excludes deleted transactions.
*   **User Story:** As an attacker, I attempt to read or delete another user's transaction.
    *   *AC:* API returns 404 or 403. Database queries explicitly include `user_id = $1`.