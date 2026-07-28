# Admin web UI — implementation plan

- **Date:** 2026-07-27
- **Status:** done
- **Summary:** `internal/app/adminweb` — a session-authenticated JSON BFF plus an embedded React SPA (`web/`) — served on its own port, managing connectors, routes and users through the in-process admin services.
- **Related:** [specs/001-admin-web-ui.md](../specs/001-admin-web-ui.md), [adr/002-web-ui-stack.md](../adr/002-web-ui-stack.md)

## Context

The gateway manages connectors/routes/users through in-process services (`admin.Service`, `admin.RouteService`, `admin.UserService`, built in `internal/app/gateway/runtime.go`) and a bearer-token JSON API (`internal/app/admin/handler.go`). There was no browser UI, session, or static serving. This plan added one on its own listener.

## Approach

Per ADR-002: a React/TypeScript SPA (Refine + Ant Design, Vite) in `web/`, built into `internal/app/adminweb/dist` and `go:embed`-ed; a Go BFF in the same package exposing `/api/*` (session cookie + CSRF header) that calls the admin services directly. The gateway builds the handler when `admin.web_listen_address` is set and exposes it as `Runtime.WebHandler` / `Runtime.WebListenAddress`; `main.go` runs a second `http.Server` for it.

> Steps 2–6 below replace the original server-rendered `html/template` + HTMX plan (same scope, different stack — see ADR-002).

## Steps

### Step 1: Config + second-server wiring — done

- **Files:** `internal/app/gateway/config.go` (`AdminConfig.WebListenAddress` / `WebUsername` / `WebPassword` + validation), `secrets.go` (resolve `admin.web_password` through the `env:`/`file:` secret refs), `runtime.go` (`WebHandler`/`WebListenAddress` on `Runtime`, build `adminweb.New`), `cmd/jasmin-go-httpapi/main.go` (second `http.Server`, shared graceful-shutdown ctx, shares the `https` cert when set), `configs/gateway.example.json`.
- **Verify:** `go build ./...`; config validation rejects a web listener without credentials; gateway boots and logs both listeners.

### Step 2: Session + CSRF + auth middleware — done

- **Files:** `session.go`, `csrf.go`, `auth.go`, `auth_test.go`.
- **Changes:** Signed cookie `base64(payload).base64(HMAC(key,payload))` with `payload = "username|issuedUnix"`, 12 h max age, `HttpOnly` / `SameSite=Strict` / `Secure` under TLS, boot-random key. CSRF is `HMAC(key,"csrf|"+user)`, compared constant-time from the `X-CSRF-Token` header on every non-idempotent method. `requireSession` answers **JSON 401/403 and never redirects** — the SPA owns navigation.
- **Verify:** `go test ./internal/app/adminweb/` — session round-trip, tamper/expiry/foreign-key rejection, cross-user CSRF rejection, 401 without session, 403 without CSRF.

### Step 3: BFF resource endpoints + SPA serving — done

- **Files:** `server.go` (mux), `rest.go` (JSON helpers, `writeServiceError`, `_start`/`_end` + `X-Total-Count` windowing), `handlers_auth.go` (`/api/login`, `/api/logout`, `/api/session`, `/api/health`), `spa.go` (`go:embed all:dist`, index fallback, immutable caching for `assets/`).
- **Changes:** `/api/*` is the only authenticated surface; unmatched `/api/` paths are JSON 404s and never fall through to the SPA; every other GET serves the bundle so client-side deep links work. `/api/health` renders the shared readiness probe (`Runtime.healthProbe()`, refactored out of `healthHandler` in `internal/app/gateway/health.go` so `/health` and the dashboard cannot drift).
- **Verify:** `go test ./internal/app/adminweb/ -run HealthAndSPA`; live `curl` of `/`, a deep link, and a hashed asset.

### Step 4: Connectors CRUD — done

- **Files:** `handlers_connectors.go`, `web/src/pages/connectors/*`.
- **Changes:** Flat resource shape (the `smppc.Config` JSON tags inline + `id`/`desired_started`/`observed`). The bind **password is write-only** — never serialised back, and an empty password on update keeps the stored one. Update starts from the stored config so a partial `PATCH` (e.g. the list's Start/Stop toggle sending only `desired_started`) preserves every other field. Delete **stops a running connector first** — the service refuses to remove one that is started, which would otherwise make the UI's delete button fail.
- **Verify:** `go test ./internal/app/adminweb/ -run Connectors`; live create/start/stop/edit/delete against a running gateway.

### Step 5: Users CRUD — done

- **Files:** `handlers_users.go`, `web/src/pages/users/*`.
- **Changes:** Password is entered plaintext and SHA-256-hashed server-side into `outbound.UserConfig.PasswordSHA256`; no password material is ever sent to the browser, and an empty password on update keeps the stored hash. `external_id` defaults to the username. The handler marshals the spec and calls `UserService.CreateUser` (an upsert that preserves the uid).
- **Verify:** `go test ./internal/app/adminweb/ -run Users`; live — a UI-created user authenticated against the sendsms API and sent a message.

### Step 6: Routes CRUD (+ filters) — done

- **Files:** `handlers_routes.go`, `web/src/pages/routes/*`.
- **Changes:** Order is the route's identity (`id == order`; locked on edit). The form offers the MT filter types with type-dependent inputs (`pattern` / `value` / `username` / `start`+`end`), the handler marshals an `outbound.RouteConfig` and calls `PutRoute`, and provisioner rejections surface inline as 400s.
- **Verify:** `go test ./internal/app/adminweb/ -run Routes`; live — created a filtered route, submitted a matching message, confirmed it went out over the connector.

## End-to-end verification (performed 2026-07-27)

Against a live gateway (RabbitMQ + throwaway Postgres + the repo's fake SMSC), with `admin.web_listen_address` set:

- `POST /api/login` with the right credential returns `{username, csrf_token}` and sets the session cookie; a wrong password is 401; an API call without a session is 401; a mutation without the CSRF header is 403; after `POST /api/logout` the API is 401 again.
- `GET /api/health` reported `status: ok` with `postgres` / `amqp` / `bridge` / `connector:smsc-primary=bound`.
- Created a connector (started), toggled stop/start via partial `PATCH` (other fields preserved), and deleted it **while running** in one call.
- Created user `alice` and a filtered route (order 10 → the new connector, `destination_addr ^\+49`), then submitted through the public port as that user: `Success "<msgid>"`, the fake SMSC logged `submit_sm … ESME_ROK`, and the gateway wrote the `SMS-MT [cid:smsc-b] … [content:b'hello-from-webui-user']` audit line.
- `PATCH` of the user with an empty password kept the hash (the user still authenticated afterwards) and the uid was stable.
- Config-owned (`reserved`) connectors are not admin-mutable; the SPA shell answers `/`, `/routes` and other deep links; hashed assets carry `immutable` caching, `index.html` `no-store`.

### In-browser verification (2026-07-27, via the compose stack)

Driven with Playwright against the containerised gateway (`docker-compose.gateway.yml`, UI published to host loopback):

- Login page renders username/password, authenticates, and lands on the dashboard, which shows the live probe (`amqp ok`, `bridge ok`, `connector:smsc-primary bound`, `postgres ok`).
- Created a connector through the form; it appeared in the list and reached **BOUND** against the fake SMSC. Deleted it **while running** from the row action (popconfirm → one click), which is the stop-then-delete path.
- Route form renders the connector dropdown (populated from `/api/connectors`) and the repeatable filter block: choosing `destination_addr` revealed its regex input, confirming the type-dependent fields.
- User form renders with the write-only password field. `warnWhenUnsavedChanges` correctly raised a native beforeunload dialog when navigating away from a dirty form.

**Bug found and fixed in this pass:** the login route used Refine's stock `AuthPage`, which hardcodes an **email** field (`name:"email"` + email-format validation). Typing the admin username was rejected client-side with "Invalid email address", and the form would have posted `{email, password}` while the BFF expects `{username, password}`. Replaced with a purpose-built `web/src/pages/login.tsx` using `useLogin`.

## Rollback

The UI is inert unless `admin.web_listen_address` is set — omit it and the gateway behaves exactly as before (no second server, no new routes).

## Risks

- **Auth/CSRF hand-rolled** — narrow, unit-tested implementation; constant-time compares; `HttpOnly`/`SameSite=Strict`/`Secure`.
- **Privilege boundary** — the UI can mint credentials and start connectors. Keep the listener internal/localhost; it must not be public.
- **Stale embedded bundle** — `dist/` is committed, so a `web/src` change without `npm run build` ships an old UI. Follow-up: a CI step that rebuilds and diffs.
- **Shared process** — separate `http.Server`; no shared mutable state beyond the mutex-guarded admin services.
