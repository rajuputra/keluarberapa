# KeluarBerapa – Design Guidelines

## 1. Visual Direction
*   **Overall Theme:** Simple, functional, lightweight, and not over-engineered.
*   **Framework:** Astro + plain CSS/Tailwind (based on simplicity requirements).
*   **Responsiveness:** Must be mobile-friendly as users tracking expenses on WhatsApp will likely view the dashboard on their phones.

## 2. Core Screens & Layouts
### A. Public Layout (Login / Register)
*   Clean card-based forms.
*   Clear error states matching the backend `{"error": "..."}` structure.
*   Call to action for unregistered users.

### B. Dashboard Layout (Protected)
*   **Navigation:** Sidebar or top Navbar containing links to Home (Summary), Transactions, and Settings.
*   **WhatsApp Status Banner:** Prominent indicator showing if the user's WhatsApp number is verified/connected.

### C. Specific Views
1.  **Dashboard Home (Monthly Summary):**
    *   Large metric card for **Total Expense** (Current Month).
    *   Metric for **Transaction Count**.
    *   Visual or list breakdown of **Expense by Category** (ordered by total).
2.  **Transaction List:**
    *   Data Table / List View containing: Date, Description, Category, Amount, Source (Web/WhatsApp).
    *   Basic filters (Month/Year, Category).
    *   Pagination controls.
3.  **Settings:**
    *   User profile update (Name).
    *   WhatsApp connection management (Connect/Disconnect number).

## 3. UI/UX Rules
*   Do NOT use WebSockets or loading skeletons that imply realtime connections. Normal API fetching is sufficient.
*   Money amounts must be formatted in IDR (Rp).
*   Dates should reflect the `Asia/Jakarta` timezone context gracefully.