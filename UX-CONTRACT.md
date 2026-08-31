# UX Contract

## Product context

- Audience: Internal ConfigHub administrators and project members.
- Primary jobs: Sign in; inspect and maintain project configuration; review revisions; manage project membership; administer machine access; inspect synchronized users and system status.
- Target market(s): No geographic market policy is recorded. The approved i18n design authorizes `en-US` and `zh-CN` UI locales only.
- Active locales: `en-US` and `zh-CN`, with `en-US` as fallback.
- Language/content register and native-review policy: Operational, precise language. UI translation is scoped to approved application copy; API values, project data, configuration values, user data, and server messages are not translated. No native-language reviewer is recorded in the approved sources.
- Timezone/calendar policy: Browser-local date/time rendering and native `datetime-local` semantics remain current behavior; the locale boundary does not establish a business timezone or alternative calendar.
- Accessibility target: WCAG 2.2 AA.

## Business-context sources

| Domain / scope | Authoritative source | Source type | Reviewed date |
|---|---|---|---|
| Permission model | `internal/permissions/service.go`; `docs/superpowers/specs/2026-08-28-confighub-design.md` | Implemented domain policy / approved design | 2026-08-31 |
| Data lifecycle | `docs/superpowers/specs/2026-08-28-confighub-design.md`; `internal/projects/service.go`; `internal/revisions/service.go` | Approved design / implemented domain policy | 2026-08-31 |
| Deletion / retention | `docs/superpowers/specs/2026-08-28-confighub-design.md`; `internal/projects/service.go`; `internal/revisions/service.go` | Approved design / implemented domain policy | 2026-08-31 |
| Billing / payment | Not applicable: ConfigHub’s approved scope has no billing or payment workflow. | Scope record | 2026-08-31 |
| Legal / regulatory copy | Not applicable: no legal or regulatory copy is defined for the current product routes. | Scope record | 2026-08-31 |
| Market / content conventions | `docs/superpowers/specs/2026-08-31-confighub-web-i18n-design.md` | Approved localization design | 2026-08-31 |

## Visual contract

- Project `DESIGN.md`: `DESIGN.md`.
- Token ownership model (`DESIGN.md` generated / existing runtime canonical): Existing runtime canonical (Model B).
- Runtime design-system/token source: `web/src/styles.css` custom properties and component classes.
- Mapping/export/adapters: React components consume the CSS classes and variables directly; there is no generated adapter.
- Token drift gate: `npx -p @google/design.md designmd lint DESIGN.md`, source comparison against `web/src/styles.css`, and component tests.
- Supported themes: Current light warm-neutral theme; forced-colors remains browser-operable.
- Design-context owner/review policy: Product changes that alter shared visual or workflow behavior update this contract and `DESIGN.md` together; feature-only work preserves both.

## Canonical UI Map

| Capability | Canonical owner | Source of truth | Allowed variants | Verification |
|---|---|---|---|---|
| Table Selection | No shared selection primitive exists; current registers have no generic multi-select. | Current `web/src` register implementations | Not applicable until a product workflow defines selection scope. | Existing feature tests when selection is introduced. |
| Select/Listbox | Native `<select>` controls styled by `web/src/styles.css`. | Existing forms and `styles.css` | Native only; platform-owned popup geometry is accepted. | Keyboard behavior and closed-control styling; popup is platform-owned. |
| Date | Native `input[type="datetime-local"]` in machine-token issuance. | `web/src/pages/MachineAccessPage.tsx` | Native `datetime-local`; browser/OS locale, geometry, and behavior are accepted. | Existing machine-access tests and supported-browser manual checks when locale UI lands. |
| Form | Route/feature-local forms using native controls and inline messages. | Existing page/feature components | Create and edit forms retain values on recoverable failure. | Existing page and feature tests. |
| Scrollbar | Global stylesheet baseline. | `web/src/styles.css` | Document and owned overflow surfaces; no hidden-scrollbar variant. | CSS/source review and browser check. |
| Toast | No shared toast primitive exists. | Current components | Not applicable; status/alert text is inline or route-local. | Existing component tests. |
| CRUD | Page and feature flows with API-backed updates. | Existing page/feature components and domain policy sources above | Return/stay behavior is operation-specific and preserved by this i18n project. | Existing full-flow component tests. |

## Component behavior

| Component | Default | Hover | Focus | Active | Disabled | Busy | Error |
|---|---|---|---|---|---|---|---|
| Button | Bordered or ink primary action with visible label. | Canonical tone/border change and pointer. | Global `:focus-visible` outline. | `translateY(1px)` where defined. | `not-allowed`, reduced opacity, no handler. | Current text changes without changing the control’s role; duplicate submit is guarded in relevant flows. | Inline message or dialog content; no browser dialog. |
| Icon button | No canonical icon-only button is currently implemented. | Not applicable. | If introduced, use global focus treatment and localized name. | Not applicable. | Not applicable. | Not applicable. | Not applicable. |
| Input | Labelled native field with line border and canvas surface. | Stronger border. | Copper/line focus treatment plus global outline. | Native behavior. | Reduced opacity and `not-allowed`. | Existing forms preserve dimensions. | `aria-invalid` border plus inline text where implemented. |
| Secret input | Masked native password input on login. | Same as input. | Same as input. | Native behavior. | Disabled during sign-in. | Password value clears after attempt in current login flow. | Inline alert communicates recovery. |
| Search | Feature-local search controls; no shared cross-screen search primitive. | Current component behavior. | Native focus treatment. | Native behavior. | Feature-specific. | Feature-specific. | Feature-specific recovery text. |
| Textarea | Native field with current vertical resize behavior. | Stronger border. | Same as input. | Native behavior. | Reduced opacity and `not-allowed`. | Feature-specific. | Inline field message where implemented. |
| Table/list | Ruled semantic records with explicit loading/empty/error treatments. | Linked/actionable cells use their control behavior. | Native link/button focus. | Native behavior. | Not applicable to static rows. | Loading text preserves the register context. | Inline alert/retry state where implemented. |

## Dataset navigation

- Admin tables: Current administrative registers use the existing rendered datasets; this task does not introduce pagination policy.
- Exploratory lists: Current project/configuration list behavior remains unchanged; this task does not introduce infinite-scroll or load-more policy.
- URL state (default: committed search, filters, sort, page, and page size; record any transient/sensitive/non-shareable/architecture override): Existing project environment/tab state uses URL search parameters; other dataset state is preserved as currently implemented. No new URL locale parameter is permitted.
- Page size: No user-selectable page-size contract is present.
- Empty/no-results/error/loading treatment: Preserve current route/feature-local explicit states and their retry controls.
- Back/scroll restoration: Preserve current router behavior; this locale foundation does not rebuild the route tree or page state.
- Selection scope (page / all-results), selected count, filter/sort/paging behavior, keyboard operation, bulk confirmation, and post-action focus: No generic multi-select or bulk-action workflow is currently defined.

## Flow ledger

| Operation | Trigger | Pending | Success destination | Success feedback | Failure recovery | Focus outcome | Source ref |
|---|---|---|---|---|---|---|---|
| Create | Existing page/feature create actions. | Existing submitting guard and disabled action. | Existing owning route behavior. | Existing route-local status. | Preserve entered values and current inline/dialog error behavior. | Existing component-specific outcome. | Existing page/feature tests; no change in i18n foundation. |
| Edit | Existing configuration/member/environment actions. | Existing pending state. | Existing route/feature behavior. | Existing status behavior. | Existing draft/error preservation. | Existing component-specific outcome. | Existing page/feature tests; no change in i18n foundation. |
| Delete | Existing remove/revoke actions. | Existing dialog/action guard. | Existing route/feature behavior. | Existing route-local status. | Existing confirmation dialog keeps recovery context. | Existing dialog focus restoration. | `ModalDialog.tsx` and relevant feature tests. |
| Search | Existing local feature controls where present. | Existing behavior. | Same route. | Current rendered results. | Current feature behavior. | Current input behavior. | Existing feature components; no i18n change. |
| Bulk action | No generic bulk action is currently defined. | Not applicable. | Not applicable. | Not applicable. | Not applicable. | Not applicable. | Scope record. |
| Upload/background job | No upload or background-job UI is currently defined. | Not applicable. | Not applicable. | Not applicable. | Not applicable. | Not applicable. | Scope record. |
| Cancel/back | Existing cancel, dialog-close, and router-back controls. | Existing behavior. | Existing prior route or retained form. | No additional feedback required by this foundation. | Preserve current unsaved-change behavior. | Dialog focus restores to opener where applicable. | `web/src/components/ModalDialog.tsx`; existing tests. |
| Soft-delete | No universal soft-delete contract is currently defined. | Not applicable. | Not applicable. | Not applicable. | Not applicable. | Not applicable. | Scope record. |
| Hard-delete (irreversible) | No universal hard-delete UI contract is asserted here. | Not applicable. | Not applicable. | Not applicable. | Existing domain/UI behavior governs individual flows. | Existing flow behavior. | Domain sources above; no invented policy. |

## Navigation and responsive behavior

- Route document title policy: No route-title policy is implemented in the current code; later localization work must not invent one without an approved task.
- Route error / 403 page behavior: Current app-owned not-found page remains under the authenticated shell. Non-admin access redirects to `/projects` via `RequireAdmin`; this redirect is canonical for the current routes and is not changed by localization.
- Breadcrumb/tab/route-state policy: Project environment and tab state remain in search parameters; no breadcrumb primitive exists.
- Sidebar/drawer/bottom-sheet transformation: Header navigation toggles the existing primary navigation region; no sidebar/drawer/bottom-sheet contract exists.
- Responsive table strategy: Existing table wrappers retain their current overflow behavior; do not remove actions or values at narrow widths.
- Truncation/full-value access: Current wrapping/overflow behavior remains canonical; workflow-critical actions must not be clipped.
- Focus restoration and sticky-obstruction policy: `ModalDialog` restores opener focus, menu Escape restores the navigation trigger, and language changes must not move focus or rebuild state.

## Overlays and feedback

- Dialog primitive: `web/src/components/ModalDialog.tsx` provides app-owned modal semantics, initial focus, Escape handling, tab trapping, and opener restoration.
- Destructive confirmation levels: Existing destructive dialogs name the local action; no new confirmation policy is created in this task.
- Toast placement/duration/deduplication: No shared toast system is implemented.
- Alert/banner scope and persistence: Current inline alerts/status text stay scoped to their route or component.
- Tooltip delay/dismissal: No tooltip primitive is implemented.
- Unsaved-changes behavior: Existing configuration-editor behavior remains unchanged; locale switching must preserve its state.
- Layer/z-index contract (dialog > drawer > popover > toast stacking order): Current dialog backdrop uses `z-index: 20`; no drawer/popover/toast layer is implemented, so no unstated order is claimed.

## Async and resilience

- Mutation default (pessimistic/optimistic/queued): Preserve each existing flow’s behavior; this foundation performs no data mutation.
- Idempotency and duplicate-submit policy: Existing UI guards prevent repeated submit where implemented; this i18n change has no mutation side effect.
- Auto-save/draft recovery: Existing configuration-editor draft behavior is preserved; no generic autosave is introduced.
- Offline/read-stale/write behavior: No product-wide offline contract is defined.
- Retry/backoff/timeout behavior: Existing retry controls remain route/feature-specific; the new common namespace supplies a generic Retry label only.
- Version conflict and multi-tab behavior: Existing configuration/revision handling remains unchanged.
- Session expiry/re-authentication: Existing `RequireSession` redirects to `/login` and preserves destination state; language preference is browser-local, not session-bound.
- Long-running progress and return path: No new long-running workflow is added.
- Stale-request cancellation/invalidation and pending-state ownership: Preserve existing feature-local guards and generation checks.
- Dialog/form preservation and retry after mutation failure: Preserve current dialog/form state; locale change only updates i18n/document language.

## Validation

- Schema/validation layer: Existing route/feature-local validation remains canonical.
- Trigger timing: Existing input/submit behavior remains canonical.
- Error summary/inline policy: Existing inline messages and dialog content remain canonical.
- Server error mapping: Later localization may map stable API codes/field keys only; this foundation does not expose server messages as translated copy.
- Sensitive-value handling: Existing password and issued-token handling remain unchanged; locale storage holds only a locale choice.
- `noValidate`, first-invalid focus, duplicate-submit prevention, unsaved changes, and submit recovery: Preserve current component behavior; no form behavior is altered in this task.

## Permission and clipboard

- Permission UI strategy (hide vs disable vs 403 page): Existing role-based navigation and route guards remain canonical. Unauthorized admin routes redirect to `/projects`; no 403 page is introduced.
- Clipboard copy policy (truncated preview + copy button, no secret in toast): Existing machine-token copy behavior remains unchanged; localization must not place secrets in stored locale data or feedback.
- Disabled-state explanation (tooltip with reason): Current disabled controls remain as implemented; no new tooltip system is introduced.

## Migration status (only for an inconsistent established product)

- Migration ledger location: Not applicable; this task establishes i18n and documentation foundations, not a visual migration.
- Canonical primitives and owners: `styles.css` for runtime tokens; native controls where platform-owned popups are accepted; `ModalDialog` for modals; route guards for authorization navigation.
- Current risk-prioritized slices: Locale detection, persistence safety, resource parity, document-language synchronization, then later UI translation tasks.
- Legacy import/token enforcement: No legacy token import exists.
- Rollout/rollback and removal gates: Feature is statically bundled and locally reversible by changing explicit locale; code changes follow normal repository version control.

## Verification

- Required static commands: Supported Node Vitest command, `npm run typecheck`, `npm run build`, `npx -p @google/design.md designmd lint DESIGN.md`, and the premium static audit where its findings are applicable to this foundation.
- Browser/device/locale/theme matrix: Later UI work must verify login and authenticated language controls in `en-US` and `zh-CN`, narrow layout, 200% zoom, keyboard use, and state preservation. This foundation’s tests verify document-language updates only.
- Accessibility checks: Visible focus, semantic native controls, live regions, modal behavior, and no focus movement during language changes remain required.
- Native-language/domain review and target-user evidence: No native-language/domain review is recorded; do not claim it occurred.
- Japan readiness matrix (IME, mixed scripts, normalization, long names/addresses, visual regression), when applicable: Not applicable; supported locale is Simplified Chinese, not Japanese.
- Component-state/visual regression coverage: Existing project tooling has unit/component and Playwright coverage; no new visual-regression tool is configured.
- Canonical sibling flow used for comparison: Login form, application shell, and `ModalDialog` were inspected as the existing shared surfaces.
- Project audit command/result: Recorded in the task report after execution.
- CRUD full-flow evidence: Existing feature tests remain the evidence; no CRUD behavior changes in this task.
- Failure-path evidence: Locale storage exceptions are covered by `src/i18n/locales.test.ts`; resource parity and document language are covered by the i18n tests.
