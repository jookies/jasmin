# Admin web UI stack: React SPA (Refine + Ant Design) over a Go BFF

- **Date:** 2026-07-27
- **Status:** active
- **Summary:** Build the management UI as a React/TypeScript SPA (Refine + Ant Design, Vite), talk to a session-authenticated JSON backend-for-frontend in `internal/app/adminweb`, and embed the built bundle with `go:embed` so the gateway stays a single self-contained binary.

> An earlier draft of this ADR proposed server-rendered `html/template` + vendored HTMX. It was reversed during implementation; the reasoning is recorded under **Alternatives considered → Server-rendered Go templates + HTMX**.

## Context

We need a browser UI to manage the gateway (connectors, routes, users). The admin *data* plane already exists as in-process services (`admin.Service` / `RouteService` / `UserService`) built in the gateway runtime, plus a bearer-token JSON API at `/admin/*`. The codebase is otherwise lean: stdlib `net/http` + `ServeMux`, `CGO_ENABLED=0`, one server binary, deployed as a single container image.

The decision is how to render and serve the UI, and how it reaches the admin data.

## Decision

A **React SPA** (`web/`, TypeScript strict, Vite) built on **Refine** for the CRUD resource layer and **Ant Design** for components, talking to a **Go BFF** at `/api/*` in `internal/app/adminweb` that calls the admin services directly in-process. The Vite build emits into `internal/app/adminweb/dist`, which is `go:embed`-ed and served by the same handler — so the runtime image needs no Node toolchain, no CDN, and works offline.

Concretely:

- **Auth is server-side.** `POST /api/login` compares the configured credential constant-time (SHA-256 digests of both username and password) and sets a signed session cookie (`HMAC-SHA256`, boot-ephemeral key, `HttpOnly` / `SameSite=Strict` / `Secure` under TLS). No token is ever held in the browser.
- **CSRF** is a stateless per-user token (`HMAC(key, "csrf|"+user)`) handed out by `/api/login` and `/api/session` and required in the `X-CSRF-Token` header on every mutation.
- **The BFF speaks Refine's simple-rest dialect** — list windows on `_start`/`_end` with `X-Total-Count`, `{id}` in the path, `PATCH`/`PUT` for update — so the resource pages need no custom data provider.
- **Own listener.** The UI is served on `admin.web_listen_address`, separate from the public sendsms port, by a second `http.Server`.
- **The committed `dist/`** is the build artifact: `go build` never needs npm. Rebuild with `cd web && npm run build` whenever `web/src` changes.

## Alternatives considered

### Server-rendered Go templates + HTMX (the earlier draft)

Attractive on paper — zero Node, no build step, one language — and it is what this ADR originally specified. It lost once the actual page inventory was written out. The UI is four resources with list/create/edit/delete, form validation, inline error surfacing, and typed sub-forms (a repeatable route-filter block whose fields change per filter type; a connector form over ~30 `smppc.Config` fields). With templates that is hand-rolled form state, hand-rolled validation feedback, and hand-rolled table/pagination markup on every page. Refine supplies exactly that scaffolding — `useTable`, `useForm`, notifications, auth flow, unsaved-changes guards — so the SPA route reached a *more* capable UI with less bespoke code, and the parts that matter for safety (auth, CSRF, validation) still live in Go either way.

The trade-off accepted: a Node toolchain at *build* time and a committed bundle. Neither reaches the runtime image.

### SPA consuming the existing `/admin` bearer API directly

Rejected: it would put a long-lived admin bearer token in the browser, and the bearer API's payload shapes (`{"connectors": [...]}`, opaque `spec_json` for routes/users) are wrong for a UI — every page would re-implement spec marshalling client-side. The BFF keeps the token out of the browser, builds the `outbound.RouteConfig` / `UserConfig` specs server-side from typed fields, and maps the services' sentinel errors (`ErrInvalidRequest` / `ErrConflict` / `Err*NotFound`) to statuses once.

### A second Go binary for the UI

Rejected for v1: the admin services are in-process objects in the gateway runtime; a separate binary would have to reach them over HTTP or re-open the SQLite store (a second writer). Mounting in the same process on its own listener is simpler and matches how `/admin` and `/health` are already composed.

## Consequences

### Positive

- Runtime image unchanged: no Node, no CDN, no external fonts or scripts — the bundle is embedded and served from memory.
- The admin services are called directly, so validation and error mapping exist once, and no browser-held token exists.
- Refine covers list/pagination/forms/notifications, so new resources are cheap to add (billing UI later reuses the same shape).
- The UI listener is separable and bindable to localhost, keeping the management surface off the message-submission port.

### Negative / accepted trade-offs

- **A Node toolchain is required to change the UI**, and `dist/` is committed — a UI edit means rebuilding and committing the bundle, and a stale bundle is a real failure mode (CI should rebuild and diff it).
- **Bundle size**: ~1.8 MB raw / ~570 kB gzip, dominated by Ant Design. Acceptable for an internal admin tool on a LAN; code-splitting is available if that changes.
- **Hand-rolled session + CSRF** (no framework). Small, well-understood surface, unit-tested, but security-sensitive.
- Sessions do not survive a gateway restart (the signing key is boot-ephemeral) — admins re-authenticate. A configured `web_session_secret` can make this stable later.
- The UI shares the gateway process; isolated onto its own `http.Server`/listener, but it shares memory and GC with the hot path.

### Follow-ups

- CI step that runs `npm run build` and fails if `internal/app/adminweb/dist` differs from the committed bundle.
- Optional `web_session_secret` secret ref for restart-stable sessions.
- Dedicated TLS for the UI listener (today it shares the top-level `https` block).
