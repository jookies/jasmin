# Admin web UI — browser management for the gateway

- **Date:** 2026-07-27
- **Status:** done
- **Summary:** A web UI served by the gateway on its own internal port, to manage connectors, routes, and users through the existing in-process admin services. Delivered as a React SPA over a Go BFF ([ADR-002](../adr/002-web-ui-stack.md)); see [plan 011](../plans/011-admin-web-ui.md).

## Problem

The Go gateway exposes admin operations only as a bearer-token JSON API (`/admin/*`: connectors, routes, users) — there is no browser UI. Operators provision the system either by hand-crafting JSON and `curl`ing that API, editing the config file, or falling back to the legacy Python `jcli`/REST. This is error-prone (routes and users are opaque JSON specs) and inaccessible to non-CLI operators. A basic web UI closes that gap and is a prerequisite for retiring the legacy management surfaces.

## Goals

- Log in through a browser (session cookie) and manage the live system without touching JSON or the CLI.
- **Connectors:** list with live bind status, create, edit, delete, start/stop.
- **Routes:** list (ordered), create, edit, delete — with a form that produces valid `outbound.RouteConfig` JSON (connector(s), rate, default, filters).
- **Users:** list, create, edit, delete — with a form that produces valid `outbound.UserConfig` JSON (credentials, balance, quotas), password entered once and hashed server-side.
- **Dashboard:** system health (reuse `/health` checks) + connector state summary.
- Zero new **runtime** dependencies, `CGO_ENABLED=0`, single binary — the UI bundle is embedded, so no Node is needed to build or run the Go binary (only to change the UI).
- Served on a **separate listen address** from the public sendsms API, so the management surface is not exposed on the message-submission port.

## Non-goals

- Message browsing / DLR history / live traffic dashboards (no store for it today) — later.
- Multi-user RBAC — v1 is a single admin login.
- Editing the immutable config-file ("reserved") connectors — those stay config-owned; the UI manages only the admin-store (dynamic) entities, matching the JSON API's `guardCID` rule.
- Replacing the `/admin` JSON API — the UI complements it (renders against the same in-process services).
- Billing management UI — tracked separately under billing (#1).

## Requirements

### Functional

1. **Auth.** A login page authenticates a single configured admin credential (username + password, password supplied via secret ref and compared as a hash). A signed session cookie authorizes subsequent requests; logout clears it. All UI routes except the login endpoint require a valid session.
2. **CSRF.** Every state-changing request (create/edit/delete/start/stop/login/logout) carries and validates a CSRF token bound to the session.
3. **Connectors.** List shows cid, host:port, system_id, bind type, desired vs observed status. Create/edit renders a typed `smppc.Config` form; delete and start/stop are POST actions. Reserved (config) connectors are read-only.
4. **Routes.** List shows order, type, connector(s), rate, default flag, filters. Create/edit builds an `outbound.RouteConfig` and submits it as the `SpecJSON`; delete by order.
5. **Users.** List shows username, uid, balance, submit count. Create/edit builds an `outbound.UserConfig`; password is entered plaintext and SHA-256-hashed server-side into `PasswordSHA256`; delete by username.
6. **Dashboard.** Renders the `/health` check results and a connector-status roll-up.
7. **Feedback.** Validation and conflict errors from the admin services (`ErrInvalidRequest`, `ErrConflict`, `Err*NotFound`) surface as inline, human-readable messages, not stack traces.

### Non-functional

- **Security:** session cookie is `HttpOnly`, `SameSite=Strict`, `Secure` when TLS is configured; session/CSRF secret is process-ephemeral (crypto/rand at boot) or a configured secret ref; constant-time credential comparison. The UI must be bindable to localhost/internal only.
- **Self-contained:** the built UI bundle is embedded via `go:embed`; no CDN, no external fonts/scripts (works offline, no CSP escape).
- **Server-side privilege:** the browser never holds an API token — auth is a session cookie, and route/user specs are built server-side from typed fields, not marshalled in the client.
- **Isolation:** a failure or slow render in the UI must not affect the sendsms/SMPP hot path (separate `http.Server`, separate listener).

## User flows

- **Login:** GET `/ui/login` → submit credentials → on success set session cookie, redirect to `/ui/` (dashboard); on failure re-render with an error. Unauthenticated access to any `/ui/*` redirects to login.
- **Create connector:** `/ui/connectors` list → "New" → `/ui/connectors/new` form (`smppc.Config`) → POST → `Service.CreateConnector` → redirect to list with the new row; "Start" POST → `Service.SetStarted`.
- **Create user:** `/ui/users` → "New" → form → server builds `outbound.UserConfig{PasswordSHA256: sha256(pw), ...}` → `UserService.CreateUser(username, specJSON)` → redirect.
- **Create route:** `/ui/routes` → "New" → form (connector(s), rate, default, filters) → server builds `outbound.RouteConfig` JSON → `RouteService.PutRoute(order, specJSON)` → redirect.
- **Delete / edit:** row actions POST to the corresponding service method; edits pre-fill the form from `GetConnector`/`GetUser`/`GetRoute` views.

## Resolved questions

1. **Session persistence across restarts** — ephemeral boot secret; admins re-authenticate after a gateway restart. A configured `web_session_secret` remains available as a later follow-up.
2. **UI credential source** — dedicated `admin.web_username` / `admin.web_password` (the latter accepts `env:`/`file:` secret refs). The bearer `token` stays the API contract, untouched.
3. **TLS for the UI port** — the UI listener honors the existing top-level `https` block when set (and marks the session cookie `Secure`); otherwise it serves plain and must be bound internally. A dedicated `web_tls` block is a follow-up if the two ever need different certs.
