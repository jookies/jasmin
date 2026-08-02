# Topology design QA

Date: 2026-08-01
Status: active
Summary: QA pass over the topology screen rework described in [the design audit](topology-design-audit-2026-08-01.md); visual fidelity remains unverified.

- Source visual truth: [`assets/topology-mt-outbound-2026-08-01.png`](assets/topology-mt-outbound-2026-08-01.png)
- Source viewport: 3570 × 2094 px at `@2x` (approximately 1785 × 1047 CSS px).
- Intended state: MT outbound journey, analytics collapsed, whole journey fitted in the canvas.
- Implementation screenshot: unavailable because the in-app browser has no browser surface. Direct Playwright use is awaiting user approval.

## Findings addressed

- [P1] The traffic path did not read as one strong left-to-right spine.
  - Applied fixed semantic columns, a shared stage baseline, tighter column gaps, and a separate compact support band.
- [P1] Parallel and skip-column edges created glued lines, malformed curves, and crowded arrow endings.
  - Collapsed equivalent relationships, distributed terminals over four ports per side, restored short straight terminal approaches, and routed skip-column paths around blocking cards on deterministic tracks.
- [P1] Edge labels obscured their own strokes.
  - Positioned labels above or below the longest horizontal segment while leaving the path visually continuous.
- [P2] Card hierarchy was weak at fitted zoom.
  - Added a fixed two-row header, persistent context, explicit status treatment, readable child rows, and deterministic card height modeling.
- [P2] Infrastructure and supporting cards consumed excessive vertical space.
  - Moved infrastructure above the core stage and limited support cards to a compact two-row band that uses free columns beside exception stacks.
- [P2] The rail and inspector reduced map readability.
  - Reduced rail width, moved recent problems above journey choices, stopped refitting when the inspector opens, and made the inspector reflow below the canvas at narrower desktop widths.

## Required fidelity surfaces

- Typography: existing product family and weights retained; hierarchy strengthened through size, weight, and stable line allocation.
- Spacing: 208 px cards, 64 px semantic gaps, 24 px exception gaps, and a 48 px support-band gap.
- Colors: MT teal `#0f766e`; return violet and existing semantic warning/error colors retained; configuration references use quieter gray `#808d89`.
- Copy/data: existing topology names, counts, states, and problem messages retained.
- Assets: existing icon library retained; no replacement image assets were needed.

## Automated verification

- [x] TypeScript typecheck.
- [x] Seven layout regressions, including deterministic ordering and support-band collision behavior.
- [x] Five routing regressions, including port clearance, skip-column detours, terminal approach, label placement, and deterministic response ordering.
- [x] Production Vite build.
- [x] `git diff --check`.
- [ ] Same-viewport source/implementation comparison.
- [ ] Browser interaction and console-error pass.

## Comparison status

The source was inspected at original resolution. A post-implementation browser capture could not be produced with the required in-app browser, so no combined source/implementation comparison can truthfully be marked complete. The code and deterministic geometry checks pass, but final visual fidelity remains unverified.

final result: blocked
