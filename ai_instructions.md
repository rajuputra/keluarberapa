# KeluarBerapa – AI Instructions & Skills

## 1. Claude Execution Rules (System Prompts)
You are implementing KeluarBerapa according to a fixed technical specification.
1. Treat the AI Coder Handoff as the source of truth.
2. Do not invent API endpoints or database fields without explaining why.
3. Do not change architecture without explicit approval.
4. Do not introduce Redis, MQ, Microservices, OAuth, or WebSockets.
5. Do not use an LLM for expense parsing (stick to regex/rules).
6. Preserve multi-user isolation at all times. Every transaction must have a `user_id`.
7. Never trust `user_id` supplied by the frontend. Rely on JWT context.
8. Never expose passwords or password hashes. Never commit secrets.
9. Write tests together with implementation. Run tests after every stage.
10. Do not silently change the OpenAPI contract.
11. If a requirement is ambiguous, STOP and ask before making a product-level decision.

## 2. Definition of Done for Coding Agent
A stage is not complete simply because the application compiles. It is DONE only when:
1. Code compiles and tests pass.
2. Database migrations execute successfully.
3. API contract matches OpenAPI.
4. Environment variables are documented.
5. Errors are handled gracefully.
6. Multi-user isolation is explicitly tested.
7. README is updated. No secrets are committed.
8. The implementation can be run by another developer using documented steps.

## 3. Security Boundary Strictness (P0)
Data leakage between users is an absolute failure condition. 
*   WhatsApp identity resolution (`phone_number` -> `whatsapp_accounts` -> `user_id`) must be rigorous.
*   All endpoints like `PATCH /transactions/:id` must verify the transaction belongs to the JWT's `user_id`.