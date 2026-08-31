# ConfigHub Web Internationalization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add complete, persistent `zh-CN` and `en-US` localization to the ConfigHub Web administration interface without changing API, CLI, database, permission, or workflow behavior.

**Architecture:** A synchronous, statically bundled `i18next` instance sits above routing and authentication through `I18nextProvider`. Domain namespaces hold paired English and Simplified Chinese resources; a small locale boundary handles browser detection, persistence, `<html lang>`, and explicit `Intl.DateTimeFormat` locale selection. Components translate stable API codes and field keys while treating server messages and all project/configuration/user data as untrusted, untranslated data.

**Tech Stack:** React 19.2.8, TypeScript 5.9.2, Vite 8.2.2, i18next 26.4.0, react-i18next 17.0.12, Vitest 4.1.11, Testing Library, MSW, Playwright 1.62.1.

**Spec:** `docs/superpowers/specs/2026-08-31-confighub-web-i18n-design.md`

## Global Constraints

- Supported locales are exactly `zh-CN` and `en-US`; the final fallback is `en-US`.
- A valid persisted choice wins, otherwise match `navigator.languages`/`navigator.language`; all `zh-*` map to `zh-CN`, all `en-*` map to `en-US`.
- Persist only an explicit user choice under `confighub.locale`; storage failure must not break rendering or in-memory switching.
- Update `document.documentElement.lang` on initialization and every language change.
- Ship all resources in the first Web bundle; do not add runtime locale requests.
- Localize every visible and assistive Web UI message; do not localize project/environment/configuration/user/revision/Token/build data.
- Never display raw API `message` or `fields` values; map stable `code` and field keys to local resources.
- Preserve current routes, permissions, focus management, pending/error/recovery state, unsaved drafts, and browser-local timezone semantics.
- Keep API responses, Go services, CLI output, database schema, README, and operations documentation unchanged.
- Follow red-green-refactor for every behavior-bearing task and commit each task independently.

---

## Locked File Map

### Durable design context

- `DESIGN.md`: records the existing ConfigHub visual identity, runtime token ownership, localization typography, and language-control behavior.

### Internationalization foundation

- `web/src/i18n/locales.ts`: supported-locale types, normalization, detection, safe storage reads/writes, and the storage key.
- `web/src/i18n/resources/{common,auth,projects,config,versions,members,machineAccess,system}.ts`: one namespace per product domain; each exports paired `en-US` and `zh-CN` objects.
- `web/src/i18n/resources.ts`: assembles namespace modules into the exact i18next resource shape.
- `web/src/i18n/index.ts`: creates the synchronous i18next instance, changes/persists locale, and synchronizes `<html lang>`.
- `web/src/i18n/I18nProvider.tsx`: provides the application i18next instance above all routes.
- `web/src/i18n/format.ts`: locale-explicit date/date-time formatting with localized invalid-value fallback.
- `web/src/i18n/apiErrors.ts`: maps field-key presence without copying server field values.
- `web/src/i18n/*.test.ts`: detection, storage, resource parity, formatting, and safe error-boundary tests.

### Shared UI and application root

- `web/src/components/LanguageSwitcher.tsx`: labelled native locale selector shared by login and authenticated shells.
- `web/src/main.tsx`: mounts `I18nProvider` outside `App`.
- `web/src/app/{App,AppShell}.tsx`: localized route/loading/navigation/session copy.
- `web/src/pages/LoginPage.tsx`: localized login UI and code-based error recovery.
- `web/src/components/ExactValue.tsx`: localized empty-string representation and accessible value labels supplied by callers.
- `web/src/test/setup.ts`: resets global i18n state and storage between tests.
- `web/src/styles.css`: stable language-selector layout at desktop, narrow viewport, and 200% zoom.

### Product surfaces

- `web/src/pages/{ProjectsPage,ProjectPage}.tsx`: project/environment resources, states, forms, and explicit locale dates.
- `web/src/features/config/{ConfigTable,ConfigEditor,configEditorHelpers}.tsx`: configuration viewing/editing/conflict copy and safe validation mapping.
- `web/src/features/versions/VersionList.tsx`: history/diff/rollback resources and date-time formatting.
- `web/src/features/members/ProjectMembers.tsx`: localized grant workflow, safe field-key mapping, and permission labels.
- `web/src/pages/{MembersPage,MachineAccessPage,SystemPage}.tsx`: administration registers, Token lifecycle, enum labels, and dates.
- Matching `*.test.ts(x)` files: existing English regression assertions plus targeted Chinese, state-preservation, ARIA, and raw-message non-disclosure assertions.
- `web/e2e/core-flow.spec.ts`: real-browser locale detection, switching, persistence, route continuity, and representative Chinese workflow.

---

### Task 1: Establish Design Context and Locale Infrastructure

**Files:**
- Create: `DESIGN.md`
- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Create: `web/src/i18n/locales.ts`
- Create: `web/src/i18n/locales.test.ts`
- Create: `web/src/i18n/resources/common.ts`
- Create: `web/src/i18n/resources/auth.ts`
- Create: `web/src/i18n/resources/projects.ts`
- Create: `web/src/i18n/resources/config.ts`
- Create: `web/src/i18n/resources/versions.ts`
- Create: `web/src/i18n/resources/members.ts`
- Create: `web/src/i18n/resources/machineAccess.ts`
- Create: `web/src/i18n/resources/system.ts`
- Create: `web/src/i18n/resources.ts`
- Create: `web/src/i18n/resources.test.ts`
- Create: `web/src/i18n/index.ts`
- Create: `web/src/i18n/index.test.ts`
- Create: `web/src/i18n/I18nProvider.tsx`

**Interfaces:**
- Produces: `type SupportedLocale = "en-US" | "zh-CN"`.
- Produces: `SUPPORTED_LOCALES`, `DEFAULT_LOCALE`, `LOCALE_STORAGE_KEY`.
- Produces: `resolvePreferredLocale(stored: string | null, browserLanguages: readonly string[]): SupportedLocale`.
- Produces: `safeReadLocale(storage: Pick<Storage, "getItem"> | null): SupportedLocale | null`.
- Produces: `safeWriteLocale(storage: Pick<Storage, "setItem"> | null, locale: SupportedLocale): void`.
- Produces: singleton `appI18n`, async `changeLocale(locale: SupportedLocale): Promise<void>`, and `I18nProvider`.

- [ ] **Step 1: Record the existing visual and localization contract**

Create `DESIGN.md` with this normative structure and values derived from the existing `web/src/styles.css`:

```markdown
# ConfigHub Design Context

## Product character
ConfigHub is a precise internal control ledger. Its interface is restrained, information-dense, and operational rather than promotional. The existing ledger/register vocabulary, strong typographic hierarchy, dark ink, warm neutral surfaces, green brand mark, and explicit state borders remain canonical.

## Runtime ownership
`web/src/styles.css` owns runtime color, typography, spacing, border, focus, scrollbar, and responsive tokens. Shared React components own interaction behavior. This document records intent; values must not be duplicated into a second runtime theme.

## Localization
The interface supports `en-US` and `zh-CN`. Existing system font stacks must include their platform CJK fallback; no remote font is introduced. Controls reserve enough width for both languages, must remain stable at 200% zoom, and must not truncate actions needed to complete a workflow.

## Language control
Use the shared labelled native select in the login surface and authenticated header. Options use endonyms: `English` and `简体中文`. The selector inherits canonical input borders, focus ring, foreground, and surface tokens; it does not introduce a new accent or component vocabulary.

## Accessibility and motion
Target WCAG 2.2 AA. Preserve visible keyboard focus, semantic controls, live regions, modal focus behavior, reduced-motion support, and visible scrollbars. Language changes update the document language without moving focus or rebuilding page state.
```

- [ ] **Step 2: Install locked dependencies**

Run:

```powershell
npm install --save-exact i18next@26.4.0 react-i18next@17.0.12
```

Expected: `package.json` and `package-lock.json` record the exact versions and npm exits 0.

- [ ] **Step 3: Write failing locale and resource-contract tests**

Create tests covering locale normalization, persisted-choice priority, storage exceptions, and recursive namespace parity:

```ts
it("uses a valid stored locale before browser preferences", () => {
  expect(resolvePreferredLocale("en-US", ["zh-CN"])).toBe("en-US");
});

it.each([
  [["zh-Hans-CN"], "zh-CN"],
  [["en-GB"], "en-US"],
  [["fr-FR"], "en-US"],
] as const)("maps %j to %s", (languages, expected) => {
  expect(resolvePreferredLocale(null, languages)).toBe(expected);
});

it("survives storage access failures", () => {
  const storage = { getItem: () => { throw new DOMException("blocked"); } };
  expect(safeReadLocale(storage)).toBeNull();
});

it("keeps every namespace and nested key in parity", () => {
  for (const namespace of Object.keys(resources["en-US"]) as Array<keyof typeof resources["en-US"]>) {
    expect(sortedLeafKeys(resources["zh-CN"][namespace])).toEqual(
      sortedLeafKeys(resources["en-US"][namespace]),
    );
  }
});
```

- [ ] **Step 4: Run tests and verify the missing modules**

Run: `npm test -- src/i18n/locales.test.ts src/i18n/resources.test.ts`

Expected: FAIL because `locales.ts` and `resources.ts` do not exist.

- [ ] **Step 5: Implement locale detection, safe persistence, resources, and provider**

Implement the locale boundary with exact supported values and no direct storage exception propagation:

```ts
export const SUPPORTED_LOCALES = ["en-US", "zh-CN"] as const;
export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number];
export const DEFAULT_LOCALE: SupportedLocale = "en-US";
export const LOCALE_STORAGE_KEY = "confighub.locale";

function normalizeLocale(value: string): SupportedLocale | null {
  const normalized = value.trim().toLowerCase();
  if (normalized === "zh" || normalized.startsWith("zh-")) return "zh-CN";
  if (normalized === "en" || normalized.startsWith("en-")) return "en-US";
  return null;
}

export function resolvePreferredLocale(stored: string | null, browserLanguages: readonly string[]): SupportedLocale {
  const persisted = stored === null ? null : SUPPORTED_LOCALES.find((locale) => locale === stored);
  if (persisted) return persisted;
  for (const language of browserLanguages) {
    const locale = normalizeLocale(language);
    if (locale) return locale;
  }
  return DEFAULT_LOCALE;
}
```

Initialize i18next synchronously with `initImmediate: false`, `fallbackLng: DEFAULT_LOCALE`, `supportedLngs`, `nonExplicitSupportedLngs: false`, `defaultNS: "common"`, all eight namespaces, and `interpolation.escapeValue: false`. Export `changeLocale`; it must call `appI18n.changeLanguage(locale)`, update `<html lang>`, then safely persist the explicit choice. Subscribe to `languageChanged` so initialization and test-driven changes also synchronize the root language.

Each namespace file initially exports paired empty objects except `common`, which starts with endonyms and generic actions:

```ts
export const common = {
  "en-US": { language: { label: "Language", english: "English", simplifiedChinese: "Simplified Chinese" }, actions: { cancel: "Cancel", retry: "Retry" } },
  "zh-CN": { language: { label: "语言", english: "English", simplifiedChinese: "简体中文" }, actions: { cancel: "取消", retry: "重试" } },
} as const;
```

- [ ] **Step 6: Run foundation tests and typecheck**

Run:

```powershell
npm test -- src/i18n/locales.test.ts src/i18n/resources.test.ts src/i18n/index.test.ts
npm run typecheck
```

Expected: all tests pass; typecheck exits 0; `<html lang>` tests observe `en-US` and `zh-CN` after changes.

- [ ] **Step 7: Commit**

```powershell
git add DESIGN.md web/package.json web/package-lock.json web/src/i18n
git commit -m "feat: establish web internationalization foundation"
```

### Task 2: Localize the Application Root, Login, and Shared Shell

**Files:**
- Create: `web/src/components/LanguageSwitcher.tsx`
- Create: `web/src/components/LanguageSwitcher.test.tsx`
- Modify: `web/src/main.tsx`
- Modify: `web/src/app/App.tsx`
- Modify: `web/src/app/AppShell.tsx`
- Modify: `web/src/app/AppShell.test.tsx`
- Modify: `web/src/pages/LoginPage.tsx`
- Modify: `web/src/pages/LoginPage.test.tsx`
- Modify: `web/src/components/ExactValue.tsx`
- Modify: `web/src/components/ExactValue.test.tsx`
- Modify: `web/src/i18n/resources/common.ts`
- Modify: `web/src/i18n/resources/auth.ts`
- Modify: `web/src/test/setup.ts`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: `appI18n`, `changeLocale`, `SupportedLocale`, and `I18nProvider` from Task 1.
- Produces: `<LanguageSwitcher className?: string />`.

- [ ] **Step 1: Write failing language-switch and state-continuity tests**

```tsx
it("changes the document language and persists the explicit choice", async () => {
  const user = userEvent.setup();
  render(<LanguageSwitcher />);
  await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "zh-CN");
  expect(document.documentElement.lang).toBe("zh-CN");
  expect(localStorage.getItem("confighub.locale")).toBe("zh-CN");
  expect(screen.getByRole("combobox", { name: "语言" })).toHaveValue("zh-CN");
});

it("keeps login field values while switching language", async () => {
  const user = userEvent.setup();
  renderAppAt("/login");
  await user.type(screen.getByLabelText("Username"), "admin");
  await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "zh-CN");
  expect(screen.getByLabelText("用户名")).toHaveValue("admin");
  expect(screen.getByRole("button", { name: "登录" })).toBeVisible();
});

it("does not disclose a raw API login message in Chinese", async () => {
  server.use(http.post("/api/v1/auth/login", () => HttpResponse.json({ error: { code: "invalid_credentials", message: "RAW SECRET", request_id: "req", fields: {} } }, { status: 401 })));
  await changeLocale("zh-CN");
  renderAppAt("/login");
  await userEvent.type(screen.getByLabelText("用户名"), "admin");
  await userEvent.type(screen.getByLabelText("密码"), "wrong");
  await userEvent.click(screen.getByRole("button", { name: "登录" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("用户名或密码不正确。");
  expect(screen.queryByText("RAW SECRET")).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run focused tests and observe English-only failures**

Run: `npm test -- src/components/LanguageSwitcher.test.tsx src/pages/LoginPage.test.tsx src/app/AppShell.test.tsx src/components/ExactValue.test.tsx`

Expected: FAIL because the selector and translation resources are absent.

- [ ] **Step 3: Implement shared selector and mount the provider**

Use a visible label and native select; do not use an icon-only control:

```tsx
export function LanguageSwitcher({ className = "" }: { className?: string }) {
  const { i18n, t } = useTranslation("common");
  const locale = i18n.resolvedLanguage === "zh-CN" ? "zh-CN" : "en-US";
  return (
    <label className={`language-switcher ${className}`.trim()}>
      <span>{t("language.label")}</span>
      <select value={locale} onChange={(event) => void changeLocale(event.currentTarget.value as SupportedLocale)}>
        <option value="en-US">English</option>
        <option value="zh-CN">简体中文</option>
      </select>
    </label>
  );
}
```

Wrap `<App />` with `<I18nProvider>` inside `<StrictMode>`. Reset to `en-US`, remove `confighub.locale`, and restore `<html lang="en-US">` in test cleanup so existing tests remain deterministic.

- [ ] **Step 4: Migrate global and auth copy**

Populate `common` and `auth` with exact semantic groups: navigation labels; open/close navigation; skip link; role labels; session loading; page-not-found; sign-in introduction/facts/form; sign-in pending/rate-limit/network errors; sign-out pending/failure; exact-value empty-string copy. Replace literals with `useTranslation` calls and interpolations such as:

```tsx
<NavLink to={item.to}>{t(`navigation.${item.key}`)}</NavLink>
<span className="session-role">{t(`roles.${user.role}`)}</span>
<button>{loggingOut ? t("auth:signOut.pending") : t("auth:signOut.action")}</button>
```

Keep `ConfigHub`, display names, usernames, and route paths unchanged. Put the shared selector in the login form region and authenticated header. Map login errors only by status/code; never use `APIError.message`.

- [ ] **Step 5: Add stable responsive styles**

Extend the existing form-control selector so `.language-switcher select` inherits canonical borders/focus. Give the control a stable inline size and allow the header/session group to wrap without overlap:

```css
.language-switcher { display: inline-flex; align-items: center; gap: var(--space-2); }
.language-switcher select { min-inline-size: 8.5rem; }
@media (max-width: 759px) {
  .language-switcher { inline-size: 100%; justify-content: space-between; }
  .language-switcher select { min-inline-size: 0; }
}
```

- [ ] **Step 6: Run focused regression tests**

Run:

```powershell
npm test -- src/components/LanguageSwitcher.test.tsx src/pages/LoginPage.test.tsx src/app/AppShell.test.tsx src/components/ExactValue.test.tsx
npm run typecheck
```

Expected: all tests and typecheck pass in both locale assertions.

- [ ] **Step 7: Commit**

```powershell
git add web/src/main.tsx web/src/app web/src/pages/LoginPage* web/src/components web/src/i18n/resources web/src/test/setup.ts web/src/styles.css
git commit -m "feat: localize login and application shell"
```

### Task 3: Add Safe Field Mapping and Locale-Explicit Formatting

**Files:**
- Create: `web/src/i18n/format.ts`
- Create: `web/src/i18n/format.test.ts`
- Create: `web/src/i18n/apiErrors.ts`
- Create: `web/src/i18n/apiErrors.test.ts`

**Interfaces:**
- Consumes: `SupportedLocale` from Task 1.
- Produces: `formatDate(value: string, locale: SupportedLocale, unavailable: string): string`.
- Produces: `formatDateTime(value: string, locale: SupportedLocale, unavailable: string): string`.
- Produces: `localizePresentFields<K extends string>(fields: Record<string, string>, messages: Record<K, string>): Partial<Record<K, string>>`.

- [ ] **Step 1: Write failing formatter and raw-message boundary tests**

```ts
it("formats the same instant with an explicit locale", () => {
  expect(formatDateTime("2026-08-31T04:05:00Z", "en-US", "Unavailable")).toContain("2026");
  expect(formatDateTime("2026-08-31T04:05:00Z", "zh-CN", "不可用")).toContain("2026");
});

it("uses localized fallback for invalid dates", () => {
  expect(formatDate("not-a-date", "zh-CN", "不可用")).toBe("不可用");
});

it("maps field presence without copying server values", () => {
  const mapped = localizePresentFields({ slug: "RAW SECRET", ignored: "RAW OTHER" }, { slug: "项目标识不符合要求。" });
  expect(mapped).toEqual({ slug: "项目标识不符合要求。" });
  expect(JSON.stringify(mapped)).not.toContain("RAW");
});
```

- [ ] **Step 2: Run tests and verify missing exports**

Run: `npm test -- src/i18n/format.test.ts src/i18n/apiErrors.test.ts`

Expected: FAIL because both modules are missing.

- [ ] **Step 3: Implement minimal utilities**

```ts
function format(value: string, locale: SupportedLocale, unavailable: string, options: Intl.DateTimeFormatOptions): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.valueOf())) return unavailable;
  try { return new Intl.DateTimeFormat(locale, options).format(parsed); }
  catch { return unavailable; }
}

export const formatDate = (value: string, locale: SupportedLocale, unavailable: string) =>
  format(value, locale, unavailable, { dateStyle: "medium" });
export const formatDateTime = (value: string, locale: SupportedLocale, unavailable: string) =>
  format(value, locale, unavailable, { dateStyle: "medium", timeStyle: "short" });

export function localizePresentFields<K extends string>(fields: Record<string, string>, messages: Record<K, string>): Partial<Record<K, string>> {
  return Object.fromEntries(Object.keys(messages).filter((key) => Object.hasOwn(fields, key)).map((key) => [key, messages[key as K]])) as Partial<Record<K, string>>;
}
```

- [ ] **Step 4: Run utility tests and commit**

```powershell
npm test -- src/i18n/format.test.ts src/i18n/apiErrors.test.ts
npm run typecheck
git add web/src/i18n
git commit -m "feat: add localized formatting and safe error mapping"
```

### Task 4: Localize Projects and Project Environments

**Files:**
- Modify: `web/src/i18n/resources/projects.ts`
- Modify: `web/src/pages/ProjectsPage.tsx`
- Modify: `web/src/pages/ProjectsPage.test.tsx`
- Modify: `web/src/pages/ProjectPage.tsx`
- Modify: `web/src/pages/ProjectPage.test.tsx`

**Interfaces:**
- Consumes: `formatDate`, `SupportedLocale`, `localizePresentFields`, `useTranslation("projects")`.
- Produces: fully localized project list/create/detail/environment/tab surfaces.

- [ ] **Step 1: Add failing Chinese and safe-validation tests**

```tsx
it("renders and creates projects in Simplified Chinese without translating data", async () => {
  await changeLocale("zh-CN");
  renderAppAt("/projects");
  expect(await screen.findByRole("heading", { name: "项目" })).toBeVisible();
  await userEvent.click(screen.getByRole("button", { name: "新建项目" }));
  expect(screen.getByLabelText("项目标识")).toBeVisible();
  expect(screen.getByText("Shop browser flow")).toBeVisible();
});

it("localizes project validation by field key and hides server text", async () => {
  mockCreateFailure(new APIError(422, "validation_failed", "RAW MESSAGE", "req", { slug: "RAW FIELD" }));
  await changeLocale("zh-CN");
  renderAppAt("/projects");
  await submitProjectForm();
  expect(await screen.findByText("项目标识不符合要求。")).toBeVisible();
  expect(screen.queryByText(/RAW/)).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run focused tests and observe missing Chinese resources**

Run: `npm test -- src/pages/ProjectsPage.test.tsx src/pages/ProjectPage.test.tsx`

Expected: new tests fail while existing English tests remain useful regression evidence.

- [ ] **Step 3: Populate the projects namespace and migrate both pages**

Add paired keys for page heading/summary; loading/error/empty states; project cards; updated date; create-project form; project-not-found; environment selector; project metadata; configuration/versions/members tabs; create-environment form; permission/read-only notices; validation and conflict/network recovery.

Use stable interpolation and enum keys:

```tsx
const { i18n, t } = useTranslation("projects");
const locale: SupportedLocale = i18n.resolvedLanguage === "zh-CN" ? "zh-CN" : "en-US";
<time dateTime={project.updated_at}>{formatDate(project.updated_at, locale, t("states.unavailable"))}</time>
<span>{t(`permissions.${permission}`)}</span>
```

For `422`, map only `slug`, `name`, and `description` presence to local messages. For `409`, select the local resource by stable code/status. Do not render API message/field values. Preserve URL query behavior, selection, request-generation guards, focus restoration, and modal behavior unchanged.

- [ ] **Step 4: Run page tests, namespace parity, and typecheck**

```powershell
npm test -- src/pages/ProjectsPage.test.tsx src/pages/ProjectPage.test.tsx src/i18n/resources.test.ts
npm run typecheck
```

Expected: all commands pass; English and Chinese tests preserve project/environment data verbatim.

- [ ] **Step 5: Commit**

```powershell
git add web/src/i18n/resources/projects.ts web/src/pages/ProjectsPage* web/src/pages/ProjectPage*
git commit -m "feat: localize project management"
```

### Task 5: Localize Configuration Viewing, Editing, and Conflict Recovery

**Files:**
- Modify: `web/src/i18n/resources/config.ts`
- Modify: `web/src/features/config/ConfigTable.tsx`
- Modify: `web/src/features/config/ConfigTable.test.tsx`
- Modify: `web/src/features/config/ConfigEditor.tsx`
- Modify: `web/src/features/config/ConfigEditor.test.tsx`
- Modify: `web/src/features/config/configEditorHelpers.ts`
- Modify: `web/src/features/config/configEditorHelpers.test.ts`

**Interfaces:**
- Consumes: `useTranslation("config")`, shared common actions, and locale infrastructure.
- Changes: `validateEntries(draft, messages)` accepts `{ invalidKey: string; duplicateKey: string }`.
- Changes: `mapServerValidation(fields, submittedIds, messages)` accepts localized `{ entries; message; key; value; service }` and ignores all server values.

- [ ] **Step 1: Write failing validation-boundary and state-preservation tests**

```ts
it("uses supplied validation messages", () => {
  const draft = [toDraftEntry({ key: "invalid key", value: "", service: "" })];
  expect(validateEntries(draft, { invalidKey: "INVALID_LOCAL", duplicateKey: "DUP_LOCAL" })[draft[0].id]?.key).toBe("INVALID_LOCAL");
});

it("maps server field paths without returning server values", () => {
  const result = mapServerValidation({ "entries[0].key": "RAW SECRET" }, ["row-1"], {
    entries: "ENTRIES_LOCAL", message: "MESSAGE_LOCAL", key: "KEY_LOCAL", value: "VALUE_LOCAL", service: "SERVICE_LOCAL",
  });
  expect(result.entryErrors["row-1"]?.key).toBe("KEY_LOCAL");
  expect(JSON.stringify(result)).not.toContain("RAW SECRET");
});

it("keeps an unsaved draft when switching to Chinese", async () => {
  renderEditor(revision);
  await userEvent.type(screen.getByLabelText("Value for DATABASE_URL"), "-draft");
  await changeLocale("zh-CN");
  expect(screen.getByLabelText("DATABASE_URL 的值")).toHaveValue(expect.stringContaining("-draft"));
});
```

- [ ] **Step 2: Run focused tests and verify failures**

Run: `npm test -- src/features/config/configEditorHelpers.test.ts src/features/config/ConfigEditor.test.tsx src/features/config/ConfigTable.test.tsx`

Expected: FAIL on new signatures, Chinese names, and raw-message boundary.

- [ ] **Step 3: Refactor pure validation helpers first**

Use explicit localized message arguments:

```ts
export interface EntryValidationMessages { invalidKey: string; duplicateKey: string }
export interface ServerValidationMessages { entries: string; message: string; key: string; value: string; service: string }

export function validateEntries(
  draft: DraftEntry[],
  messages: EntryValidationMessages,
): Record<string, EntryErrors> {
  const errors: Record<string, EntryErrors> = {};
  const seen = new Set<string>();
  for (const entry of draft) {
    const key = entry.key.trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/u.test(key)) {
      errors[entry.id] = { key: messages.invalidKey };
    }
    if (seen.has(key)) {
      errors[entry.id] = { ...errors[entry.id], key: messages.duplicateKey };
    }
    seen.add(key);
  }
  return errors;
}

export function mapServerValidation(
  fields: Record<string, string>,
  submittedIds: string[],
  messages: ServerValidationMessages,
): { entriesError: string; entryErrors: Record<string, EntryErrors>; messageError: string } {
  let entriesError = "";
  const entryErrors: Record<string, EntryErrors> = {};
  let messageError = "";
  for (const field of Object.keys(fields)) {
    if (field === "entries") {
      entriesError = messages.entries;
      continue;
    }
    if (field === "message") {
      messageError = messages.message;
      continue;
    }
    const match = /^entries\[(\d+)\]\.(key|value|service)$/u.exec(field);
    if (!match) continue;
    const id = submittedIds[Number(match[1])];
    const entryField = match[2] as keyof EntryErrors;
    if (id) entryErrors[id] = { ...entryErrors[id], [entryField]: messages[entryField] };
  }
  return { entriesError, entryErrors, messageError };
}
```

The implementation keeps the existing index-to-draft-ID mapping but uses `messages[entryField]`; unknown fields are ignored.

- [ ] **Step 4: Populate config resources and migrate both components**

Add paired keys for environment selection; loading/error/retry/empty/no-results; register heading; search label and clear action; columns and exact-value ARIA labels; desktop-edit restriction; draft/base version; add/delete entry; field labels; local validation; change message; save/pending/success; unsaved-leave dialog; revision conflict refresh/compare/use-base; latest/local difference labels; absent/empty/service labels.

Use interpolation for `version`, `index`, and `key`, for example:

```tsx
<label htmlFor={`draft-${entry.id}-value`}>{t("editor.fields.value", { key: label })}</label>
setSavedStatus(t("table.saved", { version: saved.version }));
```

Add an explicit localized clear button when search is non-empty; it must clear immediately and return focus to the search input. Preserve the editor's before-unload blocker, focus restoration, line-ending protection, responsive read-only behavior, conflict comparison, and exact raw configuration values.

- [ ] **Step 5: Run configuration tests and typecheck**

```powershell
npm test -- src/features/config src/components/ExactValue.test.tsx src/i18n/resources.test.ts
npm run typecheck
```

Expected: all tests pass; raw server field values never appear; switching locale preserves draft state.

- [ ] **Step 6: Commit**

```powershell
git add web/src/i18n/resources/config.ts web/src/features/config
git commit -m "feat: localize configuration workflows"
```

### Task 6: Localize Version History, Diff, and Rollback

**Files:**
- Modify: `web/src/i18n/resources/versions.ts`
- Modify: `web/src/features/versions/VersionList.tsx`
- Modify: `web/src/features/versions/VersionList.test.tsx`

**Interfaces:**
- Consumes: `formatDateTime`, `SupportedLocale`, common actions, and `useTranslation("versions")`.
- Produces: localized version register, detail, diff enum, rollback confirmation/form, and recovery states.

- [ ] **Step 1: Write failing Chinese, date-locale, and non-disclosure tests**

```tsx
it("shows Chinese version actions while preserving revision messages", async () => {
  await changeLocale("zh-CN");
  renderVersionList();
  expect(await screen.findByRole("heading", { name: "版本" })).toBeVisible();
  expect(screen.getByText("发布 中文 😀")).toBeVisible();
  expect(screen.getByRole("button", { name: "查看版本 1" })).toBeVisible();
});

it("uses a local rollback validation message", async () => {
  api.post.mockRejectedValue(new APIError(422, "validation_failed", "RAW SECRET", "req", { message: "RAW FIELD" }));
  await changeLocale("zh-CN");
  await submitRollback();
  expect(await screen.findByRole("alert")).toHaveTextContent("请输入有效的回滚说明。");
  expect(screen.queryByText(/RAW/)).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run the version suite and observe failures**

Run: `npm test -- src/features/versions/VersionList.test.tsx`

Expected: FAIL on untranslated headings/actions and raw field handling.

- [ ] **Step 3: Populate resources and migrate VersionList**

Add paired keys for choose-environment; register loading/error/empty; version row metadata/actions; selected-detail states; diff title and added/changed/deleted labels; before/current sides; absent/empty/service labels; rollback explanation/message/validation/pending/success/failure.

Replace the local `formatDateTime` with the shared formatter and explicit locale. For `422 fields.message`, check only that the key exists and use `versions:rollback.validation.message`; do not assign the server value. Keep stale-request generation checks, exact config values, rollback confirmation, and refresh callback unchanged.

- [ ] **Step 4: Run tests, typecheck, and commit**

```powershell
npm test -- src/features/versions/VersionList.test.tsx src/i18n/format.test.ts src/i18n/resources.test.ts
npm run typecheck
git add web/src/i18n/resources/versions.ts web/src/features/versions
git commit -m "feat: localize version history"
```

### Task 7: Localize Global and Project Member Registers

**Files:**
- Modify: `web/src/i18n/resources/members.ts`
- Modify: `web/src/pages/MembersPage.tsx`
- Modify: `web/src/pages/MembersPage.test.tsx`
- Modify: `web/src/features/members/ProjectMembers.tsx`
- Modify: `web/src/features/members/ProjectMembers.test.tsx`

**Interfaces:**
- Consumes: `formatDateTime`, `localizePresentFields`, common role/permission labels, and `useTranslation("members")`.
- Changes: `memberMutationError(error, action, messages)` receives localized action-specific messages and never reads API text values.

- [ ] **Step 1: Write failing Chinese grant and raw-field tests**

```tsx
it("localizes permissions without changing usernames", async () => {
  await changeLocale("zh-CN");
  renderMembers("admin", true);
  expect(await screen.findByRole("heading", { name: "项目成员" })).toBeVisible();
  expect(screen.getByText("@alex.dev")).toBeVisible();
  expect(screen.getByRole("option", { name: "编辑者" })).toHaveValue("editor");
});

it("does not render project-member server field messages", async () => {
  rejectAdd(new APIError(422, "validation_failed", "RAW MESSAGE", "req", { username: "RAW FIELD" }));
  await changeLocale("zh-CN");
  await submitMemberAdd();
  expect(await screen.findByText("请输入已同步的有效用户名。")).toBeVisible();
  expect(screen.queryByText(/RAW/)).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run both member suites and observe failures**

Run: `npm test -- src/pages/MembersPage.test.tsx src/features/members/ProjectMembers.test.tsx`

Expected: FAIL on Chinese copy, enum labels, and safe field messages.

- [ ] **Step 3: Populate member resources and migrate both registers**

Add paired keys for synchronized directory heading; load/error/retry/empty; account role/status/update date; access-register heading; read-only note; add form; viewer/editor labels; row busy/save/remove actions; reconciliation states; status announcements; confirmation dialog; not-found/conflict/network failure recovery.

Replace `titleCase` presentation with resource lookup:

```tsx
const permissionLabel = (permission: MemberPermission) => t(`permissions.${permission}`);
setStatus(t("project.status.permissionUpdated", { name: member.display_name, permission: permissionLabel(permission) }));
```

Map `username` and `permission` field presence to local resources and ignore server values. Preserve request-generation, reconciliation, draft preservation, status focus, and immediate-removal confirmation behavior.

- [ ] **Step 4: Run tests, typecheck, and commit**

```powershell
npm test -- src/pages/MembersPage.test.tsx src/features/members/ProjectMembers.test.tsx src/i18n/resources.test.ts
npm run typecheck
git add web/src/i18n/resources/members.ts web/src/pages/MembersPage* web/src/features/members
git commit -m "feat: localize member administration"
```

### Task 8: Localize Machine Access and Token Lifecycle

**Files:**
- Modify: `web/src/i18n/resources/machineAccess.ts`
- Modify: `web/src/pages/MachineAccessPage.tsx`
- Modify: `web/src/pages/MachineAccessPage.test.tsx`

**Interfaces:**
- Consumes: `formatDateTime`, `localizePresentFields`, common actions, and `useTranslation("machineAccess")`.
- Changes: validation helpers accept localized messages or return stable keys; `tokenState` presentation uses resources while the API token state remains unchanged.

- [ ] **Step 1: Write failing Chinese lifecycle and secret-safety tests**

```tsx
it("localizes identity and Token actions while preserving machine data", async () => {
  await changeLocale("zh-CN");
  renderAdminAt();
  expect(await screen.findByRole("heading", { name: "机器访问" })).toBeVisible();
  expect(screen.getByText("shop-browser-ci")).toBeVisible();
  expect(screen.getByRole("button", { name: "签发 Token" })).toBeVisible();
});

it("localizes validation without exposing server details or issued secrets", async () => {
  rejectIssue(new APIError(422, "validation_failed", "RAW SECRET detail", "req-token", { name: "RAW FIELD" }));
  await changeLocale("zh-CN");
  await submitIssueToken();
  expect(await screen.findByRole("alert")).toHaveTextContent("Token 名称不符合要求。");
  expect(screen.queryByText(/RAW SECRET|RAW FIELD/)).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run the machine-access suite and observe failures**

Run: `npm test -- src/pages/MachineAccessPage.test.tsx`

Expected: FAIL on Chinese UI, state labels, validation mapping, and dates.

- [ ] **Step 3: Refactor validation to stable local messages**

Change `validateName`, `validateDescription`, `validateIdentity`, `validateExpiry`, and `mapFieldErrors` so callers supply translated messages or the helpers return stable keys. Keep UTF-8 byte limits and Go-space trimming exactly unchanged. A representative interface is:

```ts
interface MachineValidationMessages {
  required: string;
  nameTooLong: string;
  descriptionTooLong: string;
  expiryInvalid: string;
  expiryPast: string;
}
```

For API `422`, inspect only supported field names (`name`, `description`, `expires_at`, `grants`) and substitute local messages. Never pass `caught.fields` values into JSX.

- [ ] **Step 4: Populate machine-access resources and migrate the page**

Add paired keys for page/register states; identity create/disable metadata; description and byte-limit help; grant project/environment selection and saving; Token list/table; active/expired/revoked enum labels; issue form; exact expiration; one-time Token warning/copy/copied/dismiss actions; metadata dialog; revoke confirmation/pending/recovery; dates and inaccessible/unknown states.

Use interpolation for identity names, Token names/prefixes, grant labels, byte counts, and dates. The issued Token value and all names remain exact data. Preserve copy-to-clipboard behavior, one-time plaintext lifetime, focus management, generation guards, permission scope, and confirmation behavior.

- [ ] **Step 5: Run tests, typecheck, and commit**

```powershell
npm test -- src/pages/MachineAccessPage.test.tsx src/i18n/format.test.ts src/i18n/resources.test.ts
npm run typecheck
git add web/src/i18n/resources/machineAccess.ts web/src/pages/MachineAccessPage*
git commit -m "feat: localize machine access"
```

### Task 9: Localize System Status and Complete Resource Coverage

**Files:**
- Modify: `web/src/i18n/resources/system.ts`
- Modify: `web/src/pages/SystemPage.tsx`
- Modify: `web/src/pages/SystemPage.test.tsx`
- Modify: `web/src/i18n/resources.test.ts`

**Interfaces:**
- Consumes: `formatDateTime`, common status labels, and `useTranslation("system")`.
- Produces: localized System register with raw build version preserved.

- [ ] **Step 1: Write failing Chinese status tests**

```tsx
it("localizes operational labels without changing the build version", async () => {
  await changeLocale("zh-CN");
  renderAt();
  expect(await screen.findByRole("heading", { name: "系统" })).toBeVisible();
  expect(screen.getByText("构建版本")).toBeVisible();
  expect(screen.getByText("v2026.08.31+sha.abc123")).toBeVisible();
  expect(screen.getAllByText("可用").length).toBeGreaterThan(0);
});
```

- [ ] **Step 2: Run System tests and observe failure**

Run: `npm test -- src/pages/SystemPage.test.tsx`

Expected: FAIL because the System surface remains English.

- [ ] **Step 3: Populate system resources and migrate the page**

Add paired keys for page summary; loading/error/retry; operational register heading/safety note; build/live/ready/SQLite/sync labels; available/unavailable values. Build the register inside the component so `t` is available, and replace the local formatter with `formatDateTime(value, locale, t("status.unavailable"))`. Keep runtime status validation and API data unchanged.

- [ ] **Step 4: Harden resource parity tests**

Make parity compare all leaf-key paths and fail on an empty namespace:

```ts
for (const namespace of namespaces) {
  const english = sortedLeafKeys(resources["en-US"][namespace]);
  const chinese = sortedLeafKeys(resources["zh-CN"][namespace]);
  expect(english.length, `${namespace} must not be empty`).toBeGreaterThan(0);
  expect(chinese).toEqual(english);
}
```

- [ ] **Step 5: Run tests, typecheck, and commit**

```powershell
npm test -- src/pages/SystemPage.test.tsx src/i18n/resources.test.ts
npm run typecheck
git add web/src/i18n/resources/system.ts web/src/pages/SystemPage* web/src/i18n/resources.test.ts
git commit -m "feat: localize system status"
```

### Task 10: Add Real-Browser Locale Acceptance and Final Verification

**Files:**
- Modify: `web/e2e/core-flow.spec.ts`
- Modify: `web/src/styles.css`
- Modify: `DESIGN.md` only if verification uncovers a durable control/token decision.

**Interfaces:**
- Consumes: all localized surfaces and `confighub.locale` contract.
- Produces: end-to-end evidence for browser detection, persistence, route/state continuity, narrow layout, and Chinese workflow.

- [ ] **Step 1: Add failing Playwright locale tests**

Add a separate browser context so the existing English critical path remains unchanged:

```ts
test("locale follows Chinese browser preference and persists an explicit English choice", async ({ browser }) => {
  const context = await browser.newContext({ locale: "zh-CN", ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto(`${runtimeServer.origin}/login`);
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.getByRole("button", { name: "登录" })).toBeVisible();
  await page.getByRole("combobox", { name: "语言" }).selectOption("en-US");
  await expect(page.locator("html")).toHaveAttribute("lang", "en-US");
  await page.reload();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("confighub.locale"))).toBe("en-US");
  await context.close();
});
```

Add a Chinese authenticated test that logs in, navigates Projects → System, switches locale, returns to the same URL, and verifies a typed search or form value survives the switch. Check the language selector and primary session actions at 390px width for non-overlap using bounding boxes.

- [ ] **Step 2: Run the new browser test and fix only observed localization/layout failures**

Run: `npm run e2e -- --grep "locale follows|Chinese authenticated"`

Expected before final fixes: the test identifies any missing resource, persistence, or narrow-header defect. Adjust shared resources/styles/components; do not introduce screen-local language controls.

- [ ] **Step 3: Run static user-interface string and unsafe-error audits**

Run:

```powershell
rg -n --glob '*.tsx' '>[^<{]*[A-Za-z][^<{]*<|aria-label="[A-Za-z]|placeholder="[A-Za-z]|title="[A-Za-z]' web/src
rg -n 'error\.message|error\.fields\.[A-Za-z]|set[A-Za-z]+\(error\.fields|caught\.fields\[[^]]+\]' web/src -g '*.ts' -g '*.tsx'
rg -n 'window\.(alert|confirm|prompt)\(' web/src
```

Expected: the first command has only product/data literals deliberately documented in the spec (`ConfigHub`, code identifiers rendered as data, and test IDs that are not user-facing); every user-facing match is migrated. The second and third commands return no unsafe UI usage.

- [ ] **Step 4: Run the premium static UI audit and inspect JSON evidence**

Run:

```powershell
python C:\Users\yeshaopeng\.codex\plugins\cache\openai-curated-remote\frontend-design-premium\1.4.0\skills\frontend-design-premium\scripts\audit_project.py C:\Users\yeshaopeng\workspace\config-hub --mode strict
```

Expected: exit 0 with no blocking findings. Fix findings in shared primitives/resources/styles, then rerun.

- [ ] **Step 5: Run complete project verification**

Run:

```powershell
npm run typecheck
npm test
npm run build
npm run e2e
bash ./scripts/check.sh
git diff --check
```

Expected: all commands exit 0. `scripts/check.sh` proves Go tests, Web tests/build, binaries, real Chromium, runtime API/CLI/backup acceptance, and secret non-disclosure still pass.

- [ ] **Step 6: Manually exercise production interaction states**

In a real browser, verify both `en-US` and `zh-CN` for login, Projects, a project Configuration/Versions/Members set, Machine Access, global Members, and System. Exercise loading, empty/no-results, failed request/retry, validation, confirmation, keyboard-only switching, 390px width, 200% zoom, reduced motion, long data values, and an unsaved configuration draft during locale switch. Confirm no focus jump, state loss, clipped critical action, English leakage in Chinese UI, or translated business data.

- [ ] **Step 7: Commit acceptance coverage and final fixes**

```powershell
git add DESIGN.md web
git commit -m "test: verify localized web workflows"
```

## Completion Evidence

The implementation is complete only when the final handoff reports:

- exact changed behavior and the `zh-CN`/`en-US` language decision;
- resource parity and raw-server-message non-disclosure results;
- actual output status for typecheck, Vitest, Vite build, Playwright, premium audit, and `scripts/check.sh`;
- real-browser coverage for keyboard, narrow viewport, 200% zoom, locale persistence, unsaved state, error/loading/empty states, and both languages;
- any unresolved risk, or an explicit statement that none remains within the approved Web-only scope.
