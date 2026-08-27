# KeluarBerapa – Product Requirements Document (PRD)

## 1. Objective
Build the MVP of **KeluarBerapa**, a multi-user expense tracking application where:
1. Users register through a web dashboard.
2. Users authenticate using email + password.
3. Each user can register exactly **one WhatsApp number**.
4. A single centralized WhatsApp Bot serves all users.
5. Backend identifies users from their registered WhatsApp number.
6. Users record expenses by sending messages such as: `Makan 25000`
7. The backend parses the message using a lightweight Regex/rule-based parser.
8. Valid transactions are saved immediately without confirmation.
9. Invalid messages receive an error/help response.
10. Dashboard displays monthly expense summaries and transaction history.

## 2. MVP User Flow
1. **Registration:** User registers via Web -> Registers WA Number -> WA Verified.
2. **Input Expense:** User sends "Makan 25000" to Central WA Bot.
3. **Processing:** Meta WA Cloud API -> Webhook -> Go Backend (Resolve User, Parse, Validate, Save) -> PostgreSQL.
4. **Confirmation:** WA Bot replies "✅ Makan Rp25.000".
5. **Dashboard:** User opens dashboard -> Go REST API -> PostgreSQL -> Monthly Summary.

## 3. Transaction Message Specification
**Valid MVP Format:** `[NAMA PENGELUARAN] [NOMINAL]`
Examples: `Makan 25000`, `Kopi 18000`, `Belanja bulanan 350000`.
The parser MUST treat the **last numeric token** as the amount. Everything before it becomes the description.

## 4. Amount Parsing Rules
MVP SHOULD support: `25000`, `25.000`, `25,000`, `25rb`, `25k`.
MVP MUST reject textual numbers or completely invalid formats (e.g., `Makan`, `25000`, `Makan dua puluh ribu`).

## 5. Category Rules
Default categories: `Makan`, `Transportasi`, `Hiburan`, `Tagihan`, `Belanja`, `Lainnya`.
Category assignment MAY initially use simple keyword matching. If no rule matches -> `Lainnya`.

## 6. MVP Priority (Phases)
1. **PHASE 1:** Authentication + User / WhatsApp Identity
2. **PHASE 2:** WhatsApp Input + Regex Parser + Direct Transaction Save
3. **PHASE 3:** Dashboard Monthly Summary + Transaction History
4. **PHASE 4:** Security / Integration Testing + Deployment

## 7. Post-MVP Backlog
*   **P1:** Edit/delete via WhatsApp, Custom categories, Better parser, Monthly WA report, Budget, Alerts, CSV export.
*   **P2:** Natural-language parser, Recurring expenses, Advanced analytics, Multiple WA identities, Multi-currency, Shared expenses.