# Jasmin-Go Completion Roadmap Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Довести Jasmin-Go от текущей fixture-backed библиотечной базы до (1) безопасного ограниченного production cutover четырёх внешних потоков и затем (2) полной legacy parity всех обязательных matrix rows.

**Architecture:** Продолжить strangler-подход: legacy Python остаётся frozen oracle и временным compatibility facade, а Go последовательно забирает сквозные потоки. Все ingress-протоколы сходятся в один canonical submit/routing/billing pipeline; RabbitMQ переносит сообщения, Redis хранит совместимое transient DLR/MO state, PostgreSQL становится authoritative control/idempotency/outbox store. Generic pickle не декодируется в Go: только allowlisted trusted Python bridge на явно определённых границах.

**Tech Stack:** Go 1.26.5, `amqp091-go`, trusted Python 3.12 sidecars, RabbitMQ 3.13, Redis 7.4, PostgreSQL через `pgx/v5`, SQLite для локальных/unit projections, Docker Compose, Prometheus/OTel, GitHub Actions.

---

## 1. Исходная точка и два определения «готово»

### Проверенный baseline

- Ветка: `go-rewrite`.
- Опубликованный HEAD: `c21bbc18254ee0d279e8d8f26bfbdc9dee857003`.
- Phase 2.34: опубликована, exact-SHA CI 4/4 PASS, workspace clean.
- Authoritative registry: 204 строки.
  - `MATCH`: 18.
  - `GO-COMPLETE`: 3.
  - `GO-PARTIAL`: 66.
  - `INVENTORIED`: 117.
  - Незавершено: 183/204.
- Functional flows: 0/4 завершены.
- `FIXTURE_COVERAGE.csv`: 183 case mappings, но fixtures касаются только 75/204 уникальных contract IDs; 124 незавершённых IDs не имеют oracle fixture.
- 11 строк имеют `GO-PARTIAL`, но не представлены в coverage registry: `RT-003`, `RR-010`, `RI-001`–`RI-006`, `A-010`, `H-010`, `H-011`.

### Release A — Core Cutover Ready

Система считается готовой к ограниченному production canary только когда одновременно:

1. Завершены четыре process-level потока:
   - HTTP `/send` → AMQP → SMPPc → `submit_sm_resp` → billing/DLR state;
   - DLR receipt → Redis correlation → HTTP/SMPPS callback;
   - MO `deliver_sm`/`data_sm` → multipart assembly → HTTP/SMPPS egress;
   - SMPP server bind/submit → общий routing/billing pipeline → response.
2. Versioned cutover manifest механически покрывает каждый edge четырёх flows; все его строки с `required_for_cutover=yes` имеют `MATCH`, `GO-COMPLETE` или `APPROVED_DEVIATION` с существующим `deviation_id`, approval evidence и rollback invariant.
3. Есть durable idempotency/outbox, restart/redelivery proof, observability, shadow traffic, canary и проверенный rollback.
4. Legacy остаётся rollback target на bounded soak period.

### Release B — Full Legacy Parity

Полная parity объявляется только когда все authoritative rows имеют `MATCH`, `GO-COMPLETE` или `APPROVED_DEVIATION`; отсутствуют `INVENTORIED`, `GO-PARTIAL`, `BLOCKED`; PB/jCli/REST и редкие SMPP/config/deploy surfaces также завершены.

---

## 2. Operating model

### Workers

Максимум две независимые тройки одновременно плюс один главный integrator:

- **Pod A:** implementer + oracle/test owner + adversarial reviewer — Outbound/SMPPc.
- **Pod B:** implementer + oracle/test owner + adversarial reviewer — DLR/Redis или Control Plane.
- **Primary integrator:** единолично владеет shared schemas, matrices, fixture registry, roadmap, CI, publication и LoopKey.

После стабилизации shared contracts появляются Pod C (MO/SMPPs) и Pod D (Control/Ops), но одновременно исполняются не более двух pods, чтобы не создавать конфликтующие изменения.

### Exclusive ownership

Только integrator меняет:

- `spec/compatibility/*_MATRIX.md`;
- `spec/compatibility/FIXTURE_COVERAGE.csv`;
- `spec/compatibility/CUTOVER_GRAPH.csv`;
- `scripts/compat/verify_fixtures.py`;
- `scripts/compat/validate_json_schemas.py`;
- `scripts/compat/capture_all.sh`;
- `.github/workflows/go-rewrite-compat.yml`;
- canonical cross-flow event schemas.

### Один macro publication cycle

Каждый macro-slice проходит:

1. Scope/plan с row IDs, source citations, non-goals и acceptance criteria.
2. Frozen-oracle capture и schema/verifier RED.
3. Go production-path RED tests.
4. Минимальная GREEN implementation.
5. Tier A focused tests.
6. Tier B full candidate gates.
7. Stable candidate identity.
8. Финальный Ralph audit без unresolved High/Medium.
9. Commit/push/exact-SHA CI.
10. Local = tracking = remote, clean workspace, final LoopKey.

Не запускать полный frozen regression после каждого helper; запускать один раз на стабильный macro candidate.

---

## 3. Полное разбиение текущих оставшихся 183 rows

Число 183 — проверенный baseline до решения `B-008`, а не hardcoded validator constant. Expected unfinished/primary total всегда вычисляется из authoritative matrices. Если Task 0.1 добавляет новый `B-008`, тот же атомарный change обновляет authoritative total, macro/task ownership, partition counts и coverage; validator не принимает промежуточное состояние.

| Macro | Строк | Назначение |
|---|---:|---|
| MS-1 Outbound MT closure | 38 | Настоящий `/send` → SMSC → response/billing/DLR state |
| MS-2 DLR | 12 | Receipt correlation и callbacks |
| MS-3 MO | 7 | Inbound assembly и egress |
| MS-4 SMPP server | 9 | Bind/session/submit/delivery parity |
| MS-5 Routing/interception | 31 | Полные routables, filters, routes, interceptors |
| MS-6 Control/Persistence/PB | 24 | Durable state, profiles, control API, PB facade |
| MS-7 jCli | 18 | Transcript-compatible admin facade |
| MS-8 REST | 10 | `/secure/*`, batch, scheduling |
| MS-9 Config/Ops/Deploy | 34 | Config, daemons, metrics, deployment |
| **Всего** | **183** | Без дублей и пропусков |

Authoritative ownership должен храниться не в prose-списках ниже, а в integrator-owned `spec/compatibility/ROW_OWNERSHIP.csv`:

```text
row_id,primary_macro,primary_task,dependency_tasks,subcontract_boundary
```

Каждый unfinished ID встречается в нём ровно один раз как `primary`; повторное упоминание строки в другом task означает только dependency/subcontract и не даёт права на promotion. Macro totals генерируются из этого CSV.

**Взаимоисключающая primary-macro partition:**

- **MS-1 (38):** `H-010`, `H-011`; `A-001`–`A-003`, `A-008`–`A-010`; `S-001`, `S-005`–`S-007`; `SP-001`, `SP-002`, `SP-005`–`SP-008`; `SE-001`–`SE-003`; `SC-001`–`SC-008`; `B-001`–`B-007`, `B-009`, `B-010`.
- **MS-2 (12):** `SP-003`, `A-006`, `A-007`, `RD-001`, `RD-002`, `RD-004`, `RD-005`, `HC-003`–`HC-007`.
- **MS-3 (7):** `A-004`, `A-005`, `RD-003`, `SP-004`, `SE-006`, `HC-001`, `HC-002`.
- **MS-4 (9):** `S-002`–`S-004`, `SS-001`–`SS-005`, `B-011`.
- **MS-5 (31):** `RT-001`–`RT-003`, `RF-001`–`RF-012`, `RR-001`–`RR-010`, `RI-001`–`RI-006`.
- **MS-6 (24):** `P-001`–`P-010`, `PB-001`–`PB-014`.
- **MS-7 (18):** `J-001`–`J-018`.
- **MS-8 (10):** `R-001`–`R-009`, `RD-006`.
- **MS-9 (34):** `H-013`, `C-001`–`C-016`, `D-001`–`D-009`, `O-001`–`O-008`.

**Primary-task allocation used to seed `ROW_OWNERSHIP.csv`:**

- Task 1.1: `S-001`, `SP-001`, `SP-005`–`SP-008`, `SE-001`–`SE-003`.
- Task 1.2: `H-010`, `H-011`, `A-001`, `SC-001`, `SC-002`.
- Task 1.3: `SP-002`, `SC-005`, `SC-006`, `SC-008`, `A-003`, `A-008`, `A-009`, `B-007`, `B-009`, `B-010`.
- Task 1.4: `A-002`, `B-001`–`B-006`.
- Task 1.5: `SC-003`, `SC-004`, `SC-007`, `S-005`–`S-007`, `A-010`.
- Task 2.1: `P-001`–`P-010`.
- Task 3.1: `SP-003`, `RD-001`, `RD-002`, `RD-004`, `RD-005`.
- Task 3.2: `A-006`, `A-007`, `HC-003`–`HC-007`.
- Task 4.1: `A-004`, `A-005`, `RD-003`, `SP-004`, `SE-006`, `HC-001`, `HC-002`.
- Task 4.2: `RT-001`–`RT-003`, `RF-001`–`RF-012`, `RR-001`–`RR-010`, `RI-001`–`RI-006`.
- Task 4.3: `S-002`–`S-004`, `SS-001`–`SS-005`, `B-011`.
- Task 5.1: `PB-001`–`PB-014`.
- Task 5.2: `J-001`–`J-018`.
- Task 6.1: `R-001`–`R-009`, `RD-006`.
- Task 6.2: `C-001`–`C-016`, `D-001`–`D-009`.
- Task 6.3: `H-013`, `O-001`–`O-008`.


Любые другие row IDs в task являются только dependencies; primary task отвечает за итоговый row-level evidence и promotion после завершения всех указанных dependency tasks.

---

# WAVE 0 — Contract registry hygiene

## Task 0.1: Сделать contract registry самопроверяемым

**Objective:** Устранить статусные и ID-расхождения до дальнейших promotions.

**Files:**

- Modify: `spec/compatibility/SURFACES.md`
- Modify: `spec/compatibility/CUTOVER_GRAPH.csv`
- Create: `spec/compatibility/CUTOVER_EDGES.csv`
- Create: `spec/compatibility/ROW_OWNERSHIP.csv`
- Create: `spec/compatibility/CONTRACT_ID_DECISIONS.md`
- Create: `scripts/compat/validate_contract_registry.py`
- Create: `scripts/compat/test_validate_contract_registry.py`
- Create: `scripts/compat/run_go_macro_tests.sh`
- Modify: `.github/workflows/go-rewrite-compat.yml`

**Steps:**

1. Написать RED unit tests, которые:
   - извлекают все IDs/statuses из семи matrices;
   - требуют ровно один authoritative owner для каждого ID;
   - проверяют допустимую ontology: `INVENTORIED`, `GO-PARTIAL`, `MATCH`, `GO-COMPLETE`, `APPROVED_DEVIATION`, `BLOCKED`;
   - требуют, чтобы каждый `CUTOVER_GRAPH.row_id` существовал и status совпадал с matrix;
   - требуют уникальности `(manifest_version,flow_id,edge_id,capability,row_id)`;
   - требуют exact set equality между независимым versioned `CUTOVER_EDGES.csv` и уникальными `(manifest_version,flow_id,edge_id)` в `CUTOVER_GRAPH.csv`, включая missing и extra edges;
   - для каждого edge требуют exact set equality между `CUTOVER_EDGES.required_row_ids` и `CUTOVER_GRAPH.row_id` с теми же version/flow/edge и `required_for_cutover=yes`, включая missing/extra rows;
   - требуют столько primary rows в `ROW_OWNERSHIP.csv`, сколько незавершённых IDs вычислено из matrices (baseline 183), уникального primary owner и совпадения generated macro counts с §3;
   - разрешают повторные task references только в `dependency_tasks` с явной `subcontract_boundary`;
   - запрещают macro closure, пока каждый primary task macro и все cross-macro dependency gates не имеют current candidate evidence для exact artifact;
   - обнаруживают malformed текущую строку `Operations,MATCH,H-012,...`.
2. Запустить:
   ```bash
   python3 -m unittest scripts.compat.test_validate_contract_registry -v
   ```
   Expected: FAIL на ontology/CUTOVER_GRAPH.
3. Расширить `CUTOVER_GRAPH.csv` колонками:
   ```text
   manifest_version,flow_id,edge_id,capability,row_id,current_status,required_for_cutover,dependencies,oracle_surface,go_packages,owner,tier_b_scope,tier_c_required,deviation_id,approval_evidence,rollback_invariant
   ```
4. Создать независимый закрытый edge manifest:
   ```text
   manifest_version,flow_id,edge_id,from_boundary,to_boundary,required_row_ids,required_post_state
   ```
   Он перечисляет каждый boundary transition четырёх Release A flows и version-controlled независимо от graph row status. Validator запрещает graph с missing/extra edge, неизвестным row ID и любым расхождением required-row sets в обе стороны.
5. Разрешить `B-008`:
   - сравнить legacy source/tests с `Q-008` и Phase 2 billing plan;
   - если float rounding — самостоятельный публичный contract, одним commit добавить `B-008` в matrix, MS-1/Task 1.4 ownership и coverage, пересчитать authoritative/unfinished totals и regenerated macro counts;
   - если это stale alias `Q-008`, исправить historical plan ссылкой и записать решение; не создавать фиктивную строку.
6. Для 11 `GO-PARTIAL` без coverage mapping:
   - добавить реальные oracle fixtures/mappings, либо
   - понизить до `INVENTORIED`, если executable differential отсутствует.
7. Добавить validator в fixture-integrity CI.
8. Создать executable wrapper:
   ```bash
   ./scripts/compat/run_go_macro_tests.sh <registry|outbound-a|outbound-b|control|dlr|mo|routing|smpps|pb|jcli|rest|core-ops|full> <focused|candidate|release>
   ```
   Wrapper обязан поднимать pinned isolated Compose services, ждать readiness, экспортировать реальные URL через environment, запускать exact focused/race/live packages и всегда выполнять teardown. Режим `core-ops` не включает PB/jCli/REST.
9. Запустить:
   ```bash
   python3 scripts/compat/validate_contract_registry.py
   python3 scripts/compat/verify_fixtures.py
   python3 scripts/build_test_manifest.py --check
   ```
   Expected: PASS и механически согласованные totals.
10. Commit: `test: validate compatibility contract registry`.

**Exit gate:** Counts выводятся из matrices автоматически; roadmap/CUTOVER_GRAPH не могут расходиться с registry.

---

# WAVE 1 — Первый настоящий Outbound MT flow

## Task 1.1: Зафиксировать canonical SubmitSM decode boundary

**Primary rows:** `S-001`, `SP-001`, `SP-005`–`SP-008`, `SE-001`–`SE-003`. **Dependencies:** `A-002`, `B-002`; inbound halves of `SP-006`/`SP-008` complete in Task 3.1 before promotion.

**Objective:** Не отправлять protocol-2 pickle как `short_message`; преобразовывать allowlisted legacy `SubmitSM` в полный canonical PDU.

**Files:**

- Modify: `internal/transport/picklecompat/bridge.go`
- Modify: `internal/transport/picklecompat/types.go`
- Create: `internal/transport/picklecompat/submit_decoder.go`
- Create: `internal/transport/picklecompat/submit_decoder_test.go`
- Modify: `internal/core/smppc/session.go`
- Modify: `internal/core/smppc/connector.go`
- Modify: `internal/transport/smppwire/types.go`
- Modify: `internal/transport/smppwire/codec.go`
- Modify: `internal/transport/smppwire/golden_test.go`
- Modify: `scripts/pickle_bridge.py`
- Create/modify matching fixture capture/schema/coverage artifacts.

**Steps:**

1. Capture legacy cases for source/destination TON/NPI, data coding, priority, schedule, validity, registered delivery, SAR, UDH, standard/custom/unknown TLVs and binary payload.
2. Write RED tests proving current `Session.Submit` incorrectly treats pickle bytes as `ShortMessage`.
3. Add narrow interface:
   ```go
   type SubmitDecoder interface {
       DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, error)
   }
   ```
4. Implement only allowlisted `smpp.pdu.operations.SubmitSM` decoding in trusted bridge; reject unknown classes/oversized fields/non-finite values.
5. Inject decoder into connector/session. Remove direct `ShortMessage: envelope.Body()` production construction.
6. Preserve `Q-015` and `Q-016` boundaries; do not silently improve vendor TLV behavior.
7. Run:
   ```bash
   PYTHON_PATH="$HOME/.hermes/hermes-agent/venv/bin/python" go test -count=20 ./internal/transport/picklecompat ./internal/transport/smppwire ./internal/core/smppc
   PYTHON_PATH="$HOME/.hermes/hermes-agent/venv/bin/python" go test -race -count=5 ./internal/core/smppc
   ```
8. Commit: `feat: decode legacy SubmitSM envelopes before SMPP write`.

**Exit gate:** Socket bytes match oracle for every supported field; invalid/unallowlisted pickle produces explicit owned settlement with no SMPP write.

## Task 1.2: Создать единый role-aware gateway composition root

**Primary rows:** `H-010`, `H-011`, `A-001`, `SC-001`, `SC-002`. **Dependencies:** `C-003`–`C-005`, `C-007`, `C-009`, `D-001`, `D-006` remain primary-owned by Task 6.2.

**Objective:** Один production executable реально запускает HTTP admission, Rabbit topology, SMPPc connectors и late billing в корректном startup/shutdown порядке.

**Files:**

- Create: `internal/app/gateway/config.go`
- Create: `internal/app/gateway/config_test.go`
- Create: `internal/app/gateway/runtime.go`
- Create: `internal/app/gateway/runtime_integration_test.go`
- Create: `cmd/jasmin-go/main.go`
- Modify: `cmd/jasmin-go-httpapi/main.go` (compatibility wrapper/deprecation path)
- Modify: `internal/app/outbound/runtime.go`
- Modify: `internal/app/outbound/config.go`
- Modify: `internal/core/smppc/manager.go`
- Modify: `internal/core/smppc/manager_test.go`

**Steps:**

1. RED tests для connector config load, duplicate CID, add/start/stop/update/status и desired/observed state.
2. Расширить `smppc.Manager` методами `Start`, `Stop`, `Update`, `Status`, `Stats`, `Reconcile`; не держать manager lock во время network shutdown.
3. Добавить connector configs в strict JSON bootstrap schema как временный compatibility bootstrap; authoritative store появится в Wave 2.
4. Startup order:
   - validate config;
   - connect durable dependencies;
   - declare topology;
   - start connector manager and wait required readiness;
   - start late billing;
   - open HTTP admission.
5. Shutdown order:
   - stop HTTP admission;
   - drain publisher/outbox;
   - stop connectors and settle/fence pending requests;
   - stop consumers;
   - close bridge/AMQP/storage.
6. Не запускать `RouterService` deliver worker, пока `processDeliverSM` stub не заменён.
7. Live RED→GREEN test: `/send` produces actual `submit_sm` at simulated SMSC and receives `submit_sm_resp`.
8. Run:
   ```bash
   go test -race -count=10 ./internal/app/gateway ./internal/core/smppc
   go build ./cmd/jasmin-go
   ```
9. Commit: `feat: compose HTTP and SMPP client gateway runtime`.

**Exit gate:** Чистый RabbitMQ + SMSC simulator принимает message от публичного `/send`; queue не остаётся без Go consumer.

## Task 1.3: Durable submit response transaction

**Primary rows:** `SP-002`, `SC-005`, `SC-006`, `SC-008`, `A-003`, `A-008`, `A-009`, `B-007`, `B-009`, `B-010`. **Dependencies:** Task 3.1 owns final `RD-001`/`RD-002` evidence.

**Objective:** Сделать response handling crash-safe для локальных side effects. Доставка к внешнему SMSC остаётся честно обозначенной как **at-least-once**: после restart/takeover любой durable intent без durable result консервативно становится `UNKNOWN_AFTER_SEND`, потому что crash до и после socket write неразличимы без SMSC idempotency; retry может привести к duplicate SMSC submit.

**Files:**

- Create: `internal/core/submittransaction/model.go`
- Create: `internal/core/submittransaction/service.go`
- Create: `internal/core/submittransaction/service_test.go`
- Create: `internal/core/submittransaction/repository.go`
- Create: `internal/infra/storage/sqlite_submittransaction.go` (unit/local projection only; production constructor must reject it)
- Create: `internal/infra/storage/sqlite_submittransaction_test.go`
- Create: `internal/infra/storage/postgres_submittransaction.go`
- Create: `internal/infra/storage/postgres_submittransaction_integration_test.go`
- Modify: `internal/core/smppc/session.go`
- Modify: `internal/core/smppc/error_retry.go`
- Modify: `internal/core/smppc/response_publish.go`
- Modify: `internal/app/outbound/late_billing_consumer.go`
- Modify: `internal/transport/amqpcompat/client.go`
- Modify: `go.mod`, `go.sum` (`pgx/v5`).

**State order:**

```text
DB transaction: get/create logical part + durable send attempt intent → commit
→ socket write (external ambiguity starts)
→ best-effort mark SENT
→ on restart/takeover any intent without durable result becomes UNKNOWN_AFTER_SEND, whether crash occurred immediately before or after write
→ correlate/classify submit_sm_resp
→ DB transaction: result + SMSC correlation + billing/DLR outbox rows → commit
→ ACK original delivery when current generation still owns it
→ outbox worker publish with broker confirm
→ mark event dispatched (re-publish is allowed; consumers deduplicate by stable event key)
```

**Steps:**

1. RED table tests for success, configured retry status, final error, timeout, consumer-generation loss, `UNKNOWN_AFTER_SEND` and restart at every transition; crash immediately before write and immediately after write must recover to the same ambiguous state when no additional durable evidence exists.
2. Define stable keys for logical submit part, send attempt and each local side-effect event; preserve opaque IDs as strings.
3. Persist attempt intent **before** socket write. On recovery, convert every unresolved intent to `UNKNOWN_AFTER_SEND`. A retry creates a new monotonically identified attempt, provides at-least-once delivery and may duplicate at SMSC; operator-visible hold is allowed only as an explicit configured deviation.
4. Implement repository transactions and unique constraints for logical part, SMSC correlation, billing application and outbox event.
5. Move `ErrorRetryPolicy` and `NewSubmitResponsePublication` from standalone projection into production response lifecycle.
6. Commit result/correlation/outbox rows atomically. Outbox worker publishes `submit.sm.resp.<CID>` and `bill_request.submit_sm_resp.<UID>` with confirms, then marks dispatched; confirmed-but-not-marked replay is tolerated through consumer deduplication.
7. ACK original delivery after durable result/outbox commit. If ACK is lost with consumer generation, redelivery must recover committed state and must not perform a fresh socket write.
8. Crash-point tests before/after intent commit, socket write, response DB commit, broker confirm, dispatched mark and ACK. Assert exactly-once **local billing/state application**, but allow and measure duplicate SMSC submits only in the ambiguous `UNKNOWN_AFTER_SEND` window.
9. Run:
   ```bash
   ./scripts/compat/run_go_macro_tests.sh outbound-a candidate
   ```
10. Commit: `feat: make submit response side effects durable and idempotent`.

**Exit gate:** Every logical part reaches `RESULT_COMMITTED` or explicit `UNKNOWN_AFTER_SEND`; local billing/correlation application is idempotent. The plan does not claim exactly-once external SMSC submission. Production runtime accepts only PostgreSQL for submit transaction/outbox; SQLite is test/local-only.

## Task 1.4: Multipart MT atomicity

**Primary rows:** `A-002`, `B-001`–`B-006`. **Dependencies:** Task 1.1 owns `SP-001`/`SE-001`–`SE-003`; existing complete `SE-004`/`SE-005` are regression dependencies; Task 1.3 owns `B-007`/`B-010`.

**Objective:** Убрать production single-part restriction без aggregate/per-part billing confusion.

**Files:**

- Modify: `internal/core/submit_service.go`
- Modify: `internal/core/segmentation/segmentation.go`
- Modify: `internal/app/outbound/submit_envelope_builder.go`
- Modify: `internal/core/submittransaction/*`
- Create: `internal/app/gateway/multipart_integration_test.go`

**Steps:**

1. RED tests: SAR, UDH, GSM7 extension, UCS2 surrogate, binary, partial publish failure, restart/redelivery.
2. Authorize aggregate once; write N part records and N outbox envelopes atomically.
3. Carry per-part bill metadata and aggregate message ID explicitly.
4. Correlate each part with its SMSC ID and derive aggregate terminal state.
5. Assert early+late debit equals oracle exactly once as a local accounting application across retries, including `UNKNOWN_AFTER_SEND`; do not infer exactly-once SMSC receipt.
6. Commit: `feat: add durable multipart outbound submission`.

**Exit gate:** Single/multipart external flow passes clean start, disconnect, retry and redelivery scenarios.

## Task 1.5: Reconnect, readiness and connector failover closure

**Primary rows:** `SC-003`, `SC-004`, `SC-007`, `S-005`–`S-007`, `A-010`. **Dependencies:** Task 1.3 owns `SC-005`/`SC-006`; Task 4.2 owns `RR-008`; Task 6.3 owns `O-003`.

**Files:**

- Modify: `internal/core/smppc/connector.go`
- Modify: `internal/core/smppc/session.go`
- Modify: `internal/core/smppc/manager.go`
- Modify: `internal/core/routepolicy/policy.go`
- Create: `internal/core/smppc/failover_integration_test.go`
- Create: `internal/core/smppc/tls_integration_test.go`

**Steps:**

1. RED fake-clock tests for initial/reconnect delay, failed bind, connection loss, readiness delay and status retry timers.
2. Add actual availability predicate from observed connector state; never treat configured connector as available merely because it exists.
3. Wire `routepolicy.Attempt` to ordered failover and exhaustion result.
4. Implement graceful unbind and TLS verification/config errors.
5. Verify configurable prefetch/concurrency effects without changing RouterPB’s separate no-QoS topology oracle.
6. Stress reconnect storm under race detector and verify no duplicate consumer/session ownership.
7. Commit: `feat: complete SMPP client lifecycle and failover`.

**Outbound production-flow gate after Task 1.5:** HTTP → SMPPc → SMSC → response/billing/DLR-state passes single/multipart, retry, reconnect and crash/redelivery tests. Это завершает внешний outbound flow, но **не** закрывает все 38 MS-1 rows: `SP-006`/`SP-008` остаются `GO-PARTIAL` до inbound production execution в Task 3.1.

---

# WAVE 2 — Durable control substrate (параллельно с Wave 1 после schema freeze)

## Task 2.1: Canonical PostgreSQL control state и legacy profile importer

**Primary rows:** `P-001`–`P-010`. **Dependencies:** Task 1.3 owns `B-009`/`B-010`.

**Files:**

- Create: `internal/control/model/`
- Create: `internal/control/repository/`
- Create: `internal/infra/storage/postgres_control.go`
- Create: `internal/infra/storage/postgres_control_integration_test.go`
- Create: `internal/infra/storage/migrations/*.sql`
- Create: `scripts/compat/export_legacy_profiles.py`
- Create: `cmd/jasmin-go-import/main.go`
- Create: `spec/schemas/control-import-v1.schema.json`

**Steps:**

1. Freeze canonical versioned schema for groups/users/credentials/connectors/routes/interceptors/quotas/desired state.
2. Write repository contract tests against SQLite and PostgreSQL.
3. Use stable external string IDs; never assign identity from JSON array position.
4. Build trusted offline Python exporter for `PROFILE.*`; Go imports canonical JSON and never writes legacy pickle.
5. Test headers, version/class-path migrations, corrupt/partial profiles, autoload and periodic persistence crash window.
6. Add backup/restore and migration idempotency tests.
7. Commit: `feat: add durable control state and profile importer`.

**Exit gate:** Restart reconstructs exact effective state and cannot duplicate/stale-map identities. **Hard dependency:** Task 2.1 PASS is mandatory before multi-instance production runtime and before Task 7.3 shadow/canary; temporary JSON bootstrap is development-only.

## Task 2.2: Versioned authenticated control API

**Rows:** substrate for `PB-002`–`PB-012`, `J-003`–`J-017`, `SC-001`, `SC-002`.

**Files:**

- Create: `internal/controlapi/server.go`
- Create: `internal/controlapi/server_test.go`
- Create: `internal/controlapi/types.go`
- Create: `internal/controlapi/auth.go`
- Create: `internal/app/control/runtime.go`
- Modify: `internal/core/smppc/manager.go`

**Steps:**

1. Define versioned admin methods for CRUD, lifecycle, status/stats, persistence/load and reconciliation.
2. Add authentication, authorization, audit records, optimistic revisions and idempotency keys.
3. Separate desired state from observed process state.
4. Add restart/reconciliation and concurrent update tests.
5. Do not expose pickle in Go API.
6. Commit: `feat: add versioned gateway control API`.

---

# WAVE 3 — DLR receipt-to-callback

## Task 3.1: SMPPc inbound demux и live Redis adapter

**Primary rows:** `SP-003`, `RD-001`, `RD-002`, `RD-004`, `RD-005`. **Dependencies:** Task 4.1 owns `SP-004`; Task 1.1 owns `SP-006`/`SP-008` and waits for their inbound halves here.

**Files:**

- Modify: `internal/core/smppc/session.go`
- Create: `internal/core/smppc/inbound_test.go`
- Create: `internal/state/rediscompat/client.go`
- Create: `internal/state/rediscompat/client_integration_test.go`
- Modify: `go.mod`, `go.sum` (`github.com/redis/go-redis/v9`).

**Steps:**

1. Capture `deliver_sm`/`data_sm` MO-vs-DLR cases, malformed receipts and full SMSC-ID corpus.
2. Add single-reader inbound dispatch for `deliver_sm` and `data_sm` plus required response PDU.
3. Implement atomic Redis read/update/delete, TTL and missing/expired handling.
4. Preserve uppercase/leading-zero/decimal/hex normalization quirk.
5. Test duplicate/reordered terminal receipts and concurrent delivery.
6. Commit: `feat: add SMPP inbound demux and Redis DLR state`.

**Final MS-1 row-closure gate after Task 3.1:** primary owner Task 1.1 пересобирает evidence для inbound halves `SP-006`/`SP-008` и запускает:

```bash
./scripts/compat/run_go_macro_tests.sh outbound-a candidate
./scripts/compat/run_go_macro_tests.sh outbound-b candidate
./scripts/compat/run_go_macro_tests.sh dlr focused
```

Только после PASS этих трёх команд validator разрешает отметить все 38 MS-1 primary rows closed. До этого допустим завершённый outbound flow, но не статус «MS-1 row closure».

## Task 3.2: DLR lookup state machine и HTTP thrower

**Rows:** `A-006`, `A-007`, `HC-003`–`HC-007`.

**Files:**

- Create: `internal/core/dlr/model.go`
- Create: `internal/core/dlr/service.go`
- Create: `internal/core/dlr/service_test.go`
- Create: `internal/core/dlr/repository.go`
- Create: `internal/infra/storage/postgres_dlr_retry.go`
- Create: `internal/infra/storage/postgres_dlr_retry_integration_test.go`
- Create: `internal/infra/storage/migrations/*_dlr_retry.sql`
- Create: `internal/app/dlrlookup/runtime.go`
- Create: `internal/app/dlrthrower/runtime.go`
- Create: `internal/transport/httpcallback/client.go`
- Create: `internal/transport/httpcallback/client_test.go`
- Modify: `internal/transport/amqpcompat/topology.go`

**Steps:**

1. Freeze level 1/2/3 callback payloads and exact GET/POST normalization.
2. Implement receipt → normalize → lookup → classify → publish thrower → retry/cleanup.
3. Success only for HTTP 200 plus exact `ACK/Jasmin`.
4. Persist bounded retry schedule; no process-local-only timer.
5. Persist callback attempts/next-at/dead-letter state in PostgreSQL and add timeout, connection error, missing ACK, duplicate receipt, DLQ, process restart and competing-worker tests.
6. Live RabbitMQ+Redis+callback-server E2E.
7. Commit: `feat: complete DLR lookup and callback flow`.

**MS-2 exit gate:** Serialized SMSC receipt reaches callback and produces exact terminal Redis/AMQP post-state.

---

# WAVE 4 — MO и SMPP server

## Task 4.1: MO classifier, multipart assembly и HTTP egress

**Rows:** `A-004`, `A-005`, `RD-003`, `SP-004`, `SE-006`, `HC-001`, `HC-002`.

**Files:**

- Modify: `internal/core/router/logic.go`
- Create: `internal/core/mo/model.go`
- Create: `internal/core/mo/assembler.go`
- Create: `internal/core/mo/assembler_test.go`
- Create: `internal/app/morouter/runtime.go`
- Create: `internal/app/mothrower/runtime.go`
- Modify: `internal/state/rediscompat/client.go`

**Steps:**

1. Replace `processDeliverSM` unconditional requeue stub with explicit classifier/dispatcher.
2. Implement non-multipart path first, then SAR/UDH assembly.
3. Preserve `longDeliverSm:<cid>:<ref>:<destination>` and fixed TTL 300 seconds.
4. Choose and document ownership: mixed-mode legacy pickle stays Python-owned or trusted bridge translates allowlisted structure; never generic Go unpickle.
5. Test missing/duplicate/reordered parts, TTL expiry, restart and exact HTTP GET/POST payload.
6. Start only implemented consumers; no runtime may subscribe to stubbed inbound queues.
7. Commit: `feat: complete inbound MO assembly and HTTP egress`.

## Task 4.2: Routing/routables/interception closure

**Rows:** `RT-001`–`RT-003`, `RF-001`–`RF-012`, `RR-001`–`RR-010`, `RI-001`–`RI-006`.

**Files:**

- Modify: `internal/core/routingfilter/*`
- Modify: `internal/core/routingtable/*`
- Modify: `internal/core/routepolicy/*`
- Modify: `internal/core/interceptor/*`
- Modify: `internal/core/submit_service.go`
- Modify: `scripts/pickle_bridge.py`

**Steps:**

1. Add missing fixtures for 11 partial rows lacking coverage before any status promotion.
2. Implement full routable/tag type semantics and invalid-boundary matrix.
3. Finish EvalPy globals/results/errors/security limits through trusted sidecar.
4. Execute interceptors through production pipeline: ordering, mutations, locks, status overrides, timeout/PB failure.
5. Preserve random-not-roundrobin quirk and non-working BestQuality route contract.
6. Add production API differential tests, not only isolated decision tests.
7. Commit: `feat: close routing and interceptor compatibility`.

## Task 4.3: SMPP server ingress

**Rows:** `S-002`–`S-004`, `SS-001`–`SS-005`, `B-011`.

**Files:**

- Create: `internal/core/smpps/session.go`
- Create: `internal/core/smpps/manager.go`
- Create: `internal/core/smpps/session_test.go`
- Create: `internal/transport/smppserver/server.go`
- Create: `internal/transport/smppserver/server_integration_test.go`
- Create: `internal/app/smpps/runtime.go`
- Modify: `cmd/jasmin-go/main.go`

**Steps:**

1. Socket RED tests for bind TX/RX/TRX, auth/IP/group checks, state-allowed commands/status codes, max/duplicate binds, unbind/ban and TLS.
2. Map protocol-specific SMPPs validation into the same canonical `core.SubmitRequest` and shared submit service used by HTTP.
3. Prove identical route/interceptor/bill/segments/downstream PDU for equivalent HTTP and SMPP requests.
4. Add MO/DLR delivery selection to RX/TRX sessions and unbound behavior.
5. Add abrupt disconnect, pending request and graceful drain tests.
6. Commit: `feat: add compatible SMPP server ingress`.

**MS-3/MS-4/MS-5 exit gate:** Socket → classify/assemble/route → HTTP/SMPPS and SMPPs submit → shared outbound both pass production-path differential tests.

---

# WAVE 5 — Compatibility control facades

## Task 5.1: Trusted Python PB facade

**Rows:** `PB-001`–`PB-014`.

**Files:**

- Create: `compat/facades/pb/`
- Create: `compat/facades/pb/tests/`
- Modify: `internal/controlapi/*`
- Create matching capture scripts, fixtures, schemas and coverage mappings.

**Steps:**

1. Capture exact auth/connect, method arguments, Deferred results, errors and pickled returns.
2. Implement trusted Python PB endpoints translating allowlisted objects to versioned Go control API.
3. Cover Router CRUD/routes/interceptors/persist/load/message APIs; SMPPc CRUD/lifecycle/submit/persist; SMPPs and interceptor APIs.
4. Run effective post-state assertions after restart/load, not transcript-only tests.
5. Do not remove PB facade until consumer inventory, migration and owner-approved deviation exist.
6. Commit: `feat: add PB compatibility facade over Go control API`.

## Task 5.2: jCli transcript facade

**Rows:** `J-001`–`J-018`.

**Files:**

- Create: `internal/transport/jclicompat/server.go`
- Create: `internal/transport/jclicompat/server_test.go`
- Create: `cmd/jasmin-go-jcli/main.go`
- Create: `compat/fixtures/jcli/`
- Create: `scripts/compat/capture_jcli_golden.py`

**Steps:**

1. Capture banner, prompts, auth, help/completion, spacing, interactive sessions and every manager command.
2. Implement mutation commands against control API; never maintain a second state store in CLI.
3. Test exact transcript plus resulting durable state.
4. Include `smppccm`, stats, persist/load/autoload and user unbind/ban.
5. Commit: `feat: add jCli compatibility facade`.

---

# WAVE 6 — Core Ops qualification и full-parity tails

Wave 6 имеет два независимых gate:

- **Core Ops qualification (до canary):** только cutover-manifest subset из `C-*`, `D-*`, `H-013`, `O-*` — production config/dependency readiness, role lifecycle, drain, core metrics/logging и deploy artifacts.
- **Full-parity tails (после bounded canary допустимо):** Task 6.1 `R-*`/`RD-006`, `O-006` jCli-dependent stats и PB/jCli/REST-dependent config/metrics, если они не помечены `required_for_cutover=yes`.

## Task 6.1: REST/batch/scheduling

**Release classification:** Full Legacy Parity; не блокирует Core Cutover, пока versioned cutover manifest не показывает активного REST consumer.

**Rows:** `R-001`–`R-009`, `RD-006`.

**Files:**

- Create: `internal/transport/restcompat/handler.go`
- Create: `internal/transport/restcompat/handler_test.go`
- Create: `internal/app/rest/runtime.go`
- Create: `internal/app/scheduler/runtime.go`
- Modify: `cmd/jasmin-go/main.go`

**Steps:**

1. Capture exact schemas/auth/errors for `/secure/send`, batch, balance, rate and ping.
2. Reuse canonical submit/rate/balance services; do not fork business logic.
3. Persist scheduling before success response; test absolute/relative time and restart.
4. Prove callback/errback cardinality, batch IDs, messageCount and QoS.
5. Commit: `feat: add REST compatibility and durable scheduling`.

## Task 6.2: Configuration and daemon roles

**Primary rows:** `C-001`–`C-016`, `D-001`–`D-009`. Primary ownership остаётся за Task 6.2, но `core-ops` wrapper исполняет только rows с `required_for_cutover=yes`; остальные branches завершаются в full-parity режиме.

**Files:**

- Create: `internal/configcompat/schema.go`
- Create: `internal/configcompat/loader.go`
- Create: `internal/configcompat/loader_test.go`
- Modify: `cmd/jasmin-go/main.go`
- Create role wrappers only where process isolation is required.
- Create: `docker/Dockerfile.go`
- Create: `kubernetes/simple-pods/jasmin-go.yml`
- Modify: `docker/README.md`
- Modify: `kubernetes/README.rst`

**Steps:**

1. Freeze every INI key/default/type plus env precedence and invalid-value behavior.
2. Add CLI flags for config path, role enable/disable and version.
3. Implement startup/partial failure/shutdown ordering and signals with bounded drain.
4. Preserve legacy ports and standalone role behavior where externally required.
5. Add Compose dependencies/readiness, volumes and non-root image.
6. Test TERM/INT, exit codes, failed dependency and graceful drain.
7. Commit: `feat: add compatible configuration and daemon lifecycle`.

## Task 6.3: Metrics, stats and privacy-safe logging

**Primary rows:** `H-013`, `O-001`–`O-008`. До canary обязательны dependency/readiness, SMPPc/SMPPs/HTTP, queue/outbox/billing/DLR/MO и privacy-log rows из manifest. `O-006` и иные jCli-dependent tails не блокируют bounded canary, если не объявлены required.

**Files:**

- Create: `internal/observability/metrics.go`
- Create: `internal/observability/metrics_golden_test.go`
- Create: `internal/observability/logging.go`
- Modify each runtime at exact increment/state transition points.
- Modify dashboards under `docker/grafana/dashboards/Jasmin/` only after metric contracts pass.

**Steps:**

1. Capture exact metric names, HELP/TYPE, labels, content type and reset semantics.
2. Instrument HTTP, SMPPc, SMPPs, users, jCli, queue/retry/outbox/DLR/MO state.
3. Preserve process-local legacy stats; add new durable/OTel metrics under separate names.
4. Redact credentials and message bodies by default; test logs for secret leakage.
5. Add liveness separate from readiness and dependency status.
6. Commit: `feat: add compatibility metrics and operational telemetry`.

---

# WAVE 7 — Parity closure and production cutover

## Task 7.1: Close remaining partial tails

**Objective:** Systematically eliminate every residual `GO-PARTIAL` not fully closed by Waves 1–6.

**Files:** Seven matrices, corresponding captures/adapters/tests; integrator-owned registry only.

**Steps:**

1. Generate machine-readable residual list from validator after every macro publication.
2. Group 4–8 related rows per closure slice; never one publication cycle per tiny helper.
3. Prioritize protocol-visible tails:
   - `/rate`, `/balance`;
   - framing/timers/TLV/unknown PDU;
   - complete AMQP topology/reconnect/QoS;
   - routing/billing error mapping and redelivery;
   - rare config/metrics branches.
4. For each row require complete textual contract coverage, production adapter execution and side-effect post-state.
5. Record any intentional behavior change only in `DEVIATIONS.md` with owner approval, migration, rollback and differential boundary.

## Task 7.2: Expand CI into production topology gates

**Files:**

- Modify: `.github/workflows/go-rewrite-compat.yml`
- Modify: `compat/compose.yaml`
- Create: `tests/integration/`
- Create: `scripts/compat/run_go_cutover_tests.sh`

**Steps:**

1. Add pinned PostgreSQL and SMSC simulator services.
2. Keep existing four jobs; add bounded jobs for:
   - outbound E2E;
   - DLR/MO Redis/Rabbit flow;
   - SMPP server socket parity;
   - migration/restart/crash recovery;
   - image/manifest smoke.
3. Require fresh isolated broker/Redis/Postgres namespaces per run.
4. Add duplicate/redelivery, reconnect storm and kill-point tests.
5. Keep exact-SHA publication gate and artifact identities.

## Task 7.3: Shadow, canary and rollback rehearsal

**Files:**

- Create: `internal/app/shadow/`
- Create: `cmd/jasmin-go-shadow/main.go`
- Create: `spec/operations/CUTOVER_RUNBOOK.md`
- Create: `spec/operations/ROLLBACK_RUNBOOK.md`
- Create: `spec/operations/SHADOW_REPORT_SCHEMA.json`
- Create: `spec/operations/cutover-criteria.yaml`
- Create: `scripts/compat/run_cutover_gate.sh`

**Steps:**

1. Import a production-like legacy state snapshot through trusted importer.
2. Run Go shadow decisions without side effects; compare routing, billing, segmentation, DLR/MO projections.
3. Canary order:
   - one connector/tenant;
   - single-part MT;
   - multipart MT;
   - DLR;
   - MO;
   - SMPP server.
4. Preflight разрешает canary только если **control state**, **submit transaction/outbox** и **DLR retry store** используют PostgreSQL. Он отклоняет SQLite/unknown backend и записывает в signed report driver/backend identity, non-secret endpoint fingerprint и применённую migration version каждого store.
5. До первого canary зафиксировать и owner-approve `cutover-criteria.yaml`: `shadow_min_messages`, `shadow_min_duration`, `canary_min_messages`, `canary_soak_duration`, queue-lag/error/DLR-age/MO-age/reconnect thresholds, `rollback_rto_seconds` и measurement windows. Значения количества/времени обязаны быть положительными; изменение после старта инвалидирует run.
6. Требовать нулевой допуск для неразрешённых routing/billing mismatches, duplicate local billing application и потерянных terminal state transitions. Разрешённый mismatch обязан ссылаться на approved `deviation_id`.
7. Rehearse rollback with pending SMPP requests, lagging outbox, active DLR retries and incomplete MO assemblies; проверить post-rollback invariants: no new Go admission, owned deliveries fenced/recovered, outbox preserved, no second local billing application, legacy health/readiness restored within `rollback_rto_seconds`.
8. `scripts/compat/run_cutover_gate.sh --criteria spec/operations/cutover-criteria.yaml` выполняет preflight/shadow/canary/rollback checks и сохраняет machine-readable signed report, artifact/config/schema/backend/migration IDs и measured windows.
9. Run bounded load/soak and backup/restore через этот gate.
10. Keep legacy target available until approved soak criteria pass.

**Core Cutover exit gate:** validator доказывает полноту cutover manifest относительно всех четырёх flow edges; `CUTOVER_GRAPH.csv` не имеет unresolved required rows; machine-readable gate report проходит approved numerical criteria; rollback post-state доказан на exact artifact/config/schema identity.

## Task 7.4: Full parity release

**Steps:**

1. Require automated matrix totals:
   ```text
   INVENTORIED=0
   GO-PARTIAL=0
   BLOCKED=0
   MATCH+GO-COMPLETE+APPROVED_DEVIATION=total
   ```
2. Run complete frozen Python regression and all Go/live differential suites.
3. Verify every approved deviation record and migration path.
4. Produce signed release manifest containing commit/tree, images, fixture hashes, schema versions, migration version and CI URLs.
5. Final Ralph audit, exact-SHA release CI, local/tracking/remote equality, clean workspace and orchestrator LoopKey.

---

## 4. Verification tiers

### Tier A — каждый change

Каждый task вызывает exact macro mode, например:

```bash
./scripts/compat/run_go_macro_tests.sh outbound-a focused
./scripts/compat/run_go_macro_tests.sh dlr focused
./scripts/compat/run_go_macro_tests.sh smpps focused
```

Wrapper содержит реальные package paths из ownership manifest, affected golden differential, `-race` для concurrent packages, fake-clock timers, invalid boundaries и schema checks. Placeholder package paths запрещены.

### Tier B — stable macro candidate

```bash
./scripts/compat/run_go_macro_tests.sh <macro> candidate
```

Candidate mode сам поднимает pinned isolated RabbitMQ/Redis/PostgreSQL/SMSC, ждёт readiness, задаёт test-only credentials/URLs через environment, выполняет teardown и запускает:

```text
exact focused + race packages
full go test ./... and go test -race ./...
go vet ./... and go build ./...
manifest/registry/fixture/schema validators
Python compatibility unit tests
fixture regeneration twice with byte comparison
macro-specific live/crash/fuzz/secret gates
```

Фактическая команда для каждого task берётся из его `primary_task → macro` mapping: Tasks 1.1–1.3=`outbound-a`, 1.4–1.5=`outbound-b`, 2.x=`control`, 3.x=`dlr`, 4.1=`mo`, 4.2=`routing`, 4.3=`smpps`, 5.1=`pb`, 5.2=`jcli`, 6.1=`rest`, 6.2–6.3=`core-ops`; Task 7.4=`full`.

### Tier C — release/cutover

- Exact remote SHA: full Go/race/vet/build + frozen Python regression.
- Migration rehearsal и backup/restore.
- Shadow differential.
- Load/soak/reconnect storm.
- Kill/restart/redelivery.
- Canary rollback.
- Local = tracking = remote; clean workspace; final-tree LoopKey.

---

## 5. Promotion rules

### `INVENTORIED → GO-PARTIAL`

Только если есть:

1. frozen-oracle case с source citation;
2. schema/verifier/coverage registration;
3. differential execution через production registry/adapter path;
4. явный список непокрытого остатка.

### `GO-PARTIAL → MATCH`

Только если покрыт весь текст строки, включая применимые wire/serialized boundaries, composition, state mutation, timers/reconnect, settlement ownership, persistence/Redis post-state, duplicate/reordered/restart и protocol-visible errors.

Unit projection, codec helper или envelope builder сами по себе не дают `MATCH`.

---

## 6. Критические риски и guards

| Риск | Severity | Guard |
|---|---|---|
| Pickled `SubmitSM` попадает в `short_message` | Critical | Task 1.1 decoder boundary + socket golden |
| HTTP process не запускает SMPPc | Critical | Task 1.2 unified composition E2E |
| ACK раньше durable billing/DLR state | Critical | Task 1.3 outbox/journal + crash matrix |
| Crash после socket write создаёт неоднозначный SMSC outcome | Critical | Durable attempt intent + `UNKNOWN_AFTER_SEND`; at-least-once claim; duplicate metric/policy |
| Redelivery вызывает двойное списание | Critical | Unique idempotency keys + restart tests |
| In-memory users/global billing queue ломают HA | Critical | Wave 2 authoritative PostgreSQL |
| Stub consumer крадёт MO/DLR и requeue-loop | High | Не подписываться до Task 4.1; role-specific consumers |
| Redis существует только как projection | High | Task 3.1 live atomic adapter |
| DLR и MO создадут разные parsers | High | Общий inbound demux до обоих flows |
| SMPP server дублирует billing pipeline | High | Shared canonical submit service |
| PB/jCli задержат core cutover | Schedule | Facades поверх control API; два release definitions |
| Big-bang switch | Critical | Shadow → bounded canary → tested rollback |
| False `MATCH` promotions | High | Registry validator + sole integrator |

---

## 7. Порядок исполнения и ориентиры

### Критический путь

1. Wave 0 registry hygiene.
2. Wave 1 Outbound decode/composition/response transaction/multipart/failover.
3. Task 2.1 durable PostgreSQL/importer hard gate.
4. Wave 3 DLR.
5. Wave 4 MO + routing + SMPP server.
6. Task 6.2/6.3 **Core Ops subset only**, через `./scripts/compat/run_go_macro_tests.sh core-ops candidate`.
7. Wave 7 shadow/canary/rollback.

### Параллельная линия

- Wave 2 durable control substrate начинается после фикса canonical submit/event IDs из Task 1.1/1.3.
- Routing closure может идти параллельно DLR после заморозки shared routable/event types.
- PB/jCli/REST не блокируют первый bounded canary, если legacy facade остаётся доступным и `CUTOVER_GRAPH` это явно фиксирует; они обязательны для Full Legacy Parity.

### Planning estimate, не обещание

При двух активных pods по три worker-роли плюс integrator:

- первый production-grade outbound flow: ориентир 3–5 macro weeks;
- Core Cutover canary: ориентир 12–18 недель после registry gate;
- Full Legacy Parity: отдельный последующий train, зависящий от PB/jCli/REST consumer inventory и 124 новых oracle surfaces.

Пересчитать сроки по фактической velocity после двух опубликованных macro cycles. Не использовать phase number или LOC как progress metric; показывать `complete/touched/remaining rows` и `flows complete/4`.

---

## 8. Следующий немедленный bounded macro

Не начинать с отдельной DLR policy. Следующий macro должен быть:

> **Outbound Completion A: registry hygiene + allowlisted SubmitSM decode + unified HTTP/SMPPc composition + live single-part response chain.**

Первый acceptance test:

```text
HTTP /send
→ production routing/billing/envelope
→ RabbitMQ submit.sm.<CID>
→ allowlisted protocol-2 SubmitSM decode
→ paced real SMPP socket write
→ submit_sm_resp
→ durable response/outbox transaction
→ original AMQP ACK
→ exact early/late billing post-state
```

После его публикации следующий bounded macro добавляет multipart, retries, reconnect/failover и crash/redelivery closure, завершая **outbound production-flow gate**. Полный 38-row MS-1 closure публикуется только после Task 3.1 и повторного composite gate для `SP-006`/`SP-008`.
