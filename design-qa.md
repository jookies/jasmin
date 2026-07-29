# Design QA — Jasmin Admin Redesign

## Evidence

- Source visual truth: the selected light operational-console concept:
  `output/playwright/source-concept-option-1.png`
- Normalized side-by-side comparison (source left, implementation right):
  `output/playwright/design-qa-comparison.png`
- Desktop login implementation:
  `output/playwright/page-2026-07-28T15-24-00-189Z.png`
- Desktop dashboard implementation:
  `output/playwright/page-2026-07-28T15-30-22-228Z.png`
- Mobile connectors implementation:
  `output/playwright/page-2026-07-28T15-33-02-134Z.png`
- Mobile navigation implementation:
  `output/playwright/page-2026-07-28T15-33-19-469Z.png`
- Final connector drawer implementation:
  `output/playwright/page-2026-07-28T15-37-15-214Z.png`
- Final post-polish dashboard:
  `output/playwright/final-dashboard.png`
- Final post-polish mobile connectors:
  `output/playwright/final-mobile-connectors.png`
- Education Center at 1440 × 1000:
  `output/playwright/education-center-desktop.png`
- Education Center at 390 × 844:
  `output/playwright/education-center-mobile.png`
- Final mobile connector table contrast at 390 × 844:
  `output/playwright/connectors-table-mobile-contrast.png`
- Partner onboarding start at 1440 × 1000:
  `output/playwright/partner-onboarding-wizard-desktop.png`
- Partner onboarding bidirectional review at 1440 × 1000:
  `output/playwright/partner-onboarding-review-desktop.png`
- Partner onboarding start at 390 × 844:
  `output/playwright/partner-onboarding-wizard-mobile.png`
- Source dimensions: 1487 × 1058 image px.
- Desktop viewport and pixels: 1440 × 1000 CSS px, 1440 × 1000 image px, device scale 1.
- Mobile viewport and pixels: 390 × 844 CSS px, 390 × 844 image px, device scale 1.
- Comparison normalization: both desktop images were proportionally scaled and centered in
  720 × 512 canvases, then placed side by side without altering either source image.
- States: signed-out login, healthy signed-in dashboard, empty connectors table, open create
  drawer, collapsed mobile navigation, open mobile navigation.

## Full-view Review

The source and implementation were inspected together in
`output/playwright/design-qa-comparison.png`. The implementation preserves the selected
direction's defining visual system: a deep navy navigation rail, warm off-white workspace,
high-contrast typography, compact operational cards, restrained semantic color, and a clear
overview-first hierarchy.

The source's historical throughput chart and sample connector rows were intentionally replaced
with real readiness-probe results and backend-backed connector state. The gateway does not expose
historical throughput data through the current admin API, so rendering the concept's chart would
fabricate operational information. This content adaptation keeps the visual hierarchy while
improving production trustworthiness.

## Focused-region Review

- Login form: labels, focus state, security copy, contrast, and field spacing are readable at
  1440 × 1000.
- Dashboard: metric hierarchy, service checks, quick actions, and operational status remain above
  the fold at 1440 × 1000.
- Source-to-dashboard: navigation proportions, content gutters, surface contrast, card hierarchy,
  status treatment, and dense operator typography remain visually aligned. The implementation
  uses teal instead of electric blue for primary accents to connect the shell to the gateway's
  existing identity and positive-status language.
- Connector drawer: fields scroll independently while the primary Save action remains visible in
  a fixed bottom action bar.
- Mobile: the page header, primary action, horizontally scrollable data table, navigation button,
  overlay, and sidebar all remain usable at 390 × 844. A visible swipe hint explains how to
  reach status and row actions without implying the columns are missing.
- Education Center: both guided tracks, MT/MO message journeys, FAQ, glossary, contextual links,
  and search preserve the console's established hierarchy at desktop and mobile widths.
- Tables: header fill, column separators, row boundaries, body text, hover state, selected state,
  and pagination controls now have stronger contrast across all resource and operations screens.
- Partner onboarding: prototype status, local-only behavior and the backend readiness gate remain
  visible throughout the flow. Connection choices progressively reveal only the inbound,
  outbound or bidirectional fields needed for that plan.

## Required Fidelity Surfaces

- Fonts and typography: system-first sans serif stack, consistent optical hierarchy, no clipped or
  truncated important text observed.
- Spacing and layout rhythm: consistent 8/12/16/24/32 rhythm, aligned page headers and cards,
  responsive gutters, no persistent control hidden by viewport overflow.
- Colors and visual tokens: high-contrast navy shell, warm neutral workspace, teal actions and
  positive states, distinct warning/error tokens.
- Image quality and assets: no raster imagery is required by this operational console. All visible
  icons use the installed Ant Design icon set; no placeholder or handcrafted SVG assets are used.
- Copy and content: operator-focused labels replace generic scaffold copy; live data and available
  capabilities are not fabricated.

## Interaction and Console Checks

- Successful operator login and redirect.
- Dashboard health probe shows 4/4 passing and 1/1 required connector bound.
- Primary navigation works on desktop.
- Mobile navigation opens with an accessible overlay state.
- Closed mobile navigation is removed from the visibility/focus tree; open navigation has an
  explicit close control, backdrop dismissal, and Escape-key dismissal.
- Connector create drawer opens and closes without submitting data.
- Save remains visible while the form body scrolls.
- Gateway-user creation now copies the username into External ID until the operator overrides it,
  and shows the resolved value plus explicit unlimited/disabled fallbacks:
  `output/playwright/create-user-defaults-final.png`
- SMPP bind creation shows the effective any-IPv4 and unlimited defaults, while all four
  permissive authorization switches start enabled:
  `output/playwright/create-smpps-defaults-final.png`
- The default-state checks were completed without saving test records.
- Browser console shows expected unauthenticated `/api/session` 401 responses while on the login
  screen. No new application errors appeared after authenticated navigation.
- Education Center search returns lessons, FAQ answers, and glossary terms; track and journey
  switches update in place; FAQ rows expand with keyboard-accessible Ant Design controls.
- Partner onboarding blocks empty required fields, preserves data across Back/Continue, renders
  both resource bundles for a bidirectional plan, and reaches a local-only completion state.
- Technical contact accepts Email, Telegram, Microsoft Teams, Phone, Other, or Not assigned yet;
  only the Email option applies email-format validation.
- Dashboard “Onboard a partner” and the Access navigation entry both open the wizard.
- A fresh authenticated wizard navigation produced no application console errors; the only
  remaining messages are the existing React Router v7 future-flag warnings in development.

## Comparison History

1. Source-to-implementation comparison:
   `output/playwright/design-qa-comparison.png`
   - No P0, P1, or P2 fidelity issues remained after accounting for backend-backed content.
   - Historical traffic visualization was excluded because no corresponding API data exists.
2. Initial drawer capture:
   `output/playwright/page-2026-07-28T15-31-09-296Z.png`
   - P1: the Save action was below the fold in long create/edit forms.
3. Fix:
   - Drawer cards now use a viewport-bounded flex layout.
   - Form content scrolls independently.
   - The action bar remains at the bottom of the visible drawer.
4. Post-fix evidence:
   `output/playwright/page-2026-07-28T15-37-15-214Z.png`
   - The Save action is visible at 1440 × 1000 while lower form fields remain scrollable.
5. Post-polish responsive evidence:
   `output/playwright/final-mobile-connectors.png`
   - Added a compact horizontal-scroll hint for dense operational tables.
   - Added an explicit mobile-menu close control and Escape handling.
   - Closed navigation no longer leaves off-canvas links in the accessibility tree.
6. Post-polish performance and identity:
   - Resource screens are route-split with a consistent loading state.
   - Browser titles now use Jasmin Gateway instead of the Refine scaffold label.
7. Education and table-contrast pass:
   - Added a dedicated, responsive learning route grounded in real Jasmin objects and workflows.
   - Verified both learning tracks, MT/MO switching, FAQ expansion, DLR search, and contextual
     navigation.
   - Increased mobile table header, divider, row, body text, hint, and pagination contrast based
     on the supplied connectors screenshot.
8. Partner onboarding prototype pass:
   - Fixed a submit-button reconciliation bug that initially skipped the Review step.
   - Fixed preserved bidirectional state so the Review step lists both inbound and outbound
     resources and endpoint details.
   - Reworked the compact progress indicator so all four step labels remain readable at 390 px.
   - Verified that the completion action creates no record and explicitly reports that nothing
     was saved or sent.

## Final Findings

- P0: none.
- P1: none.
- P2: none.
- The selected visual direction, desktop and mobile layouts, primary navigation, authentication,
  empty states, and long-form drawer behavior all pass visual and interaction review.

final result: passed
