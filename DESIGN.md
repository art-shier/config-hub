---
version: alpha
name: "ConfigHub"
description: "A restrained internal configuration-control ledger with dense operational records and explicit state boundaries."
colors:
  primary: "oklch(0.56 0.112 48)"
  ink: "oklch(0.24 0.018 220)"
  ink-soft: "oklch(0.43 0.018 215)"
  muted: "oklch(0.52 0.016 210)"
  canvas: "oklch(0.945 0.011 105)"
  surface: "oklch(0.972 0.009 95)"
  surface-quiet: "oklch(0.925 0.012 135)"
  line: "oklch(0.8 0.014 125)"
  line-strong: "oklch(0.62 0.018 180)"
  copper: "oklch(0.56 0.112 48)"
  copper-dark: "oklch(0.43 0.092 45)"
  error: "oklch(0.46 0.15 28)"
  focus: "oklch(0.52 0.13 49)"
typography:
  sans:
    fontFamily: "Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
  serif:
    fontFamily: "Georgia, 'Times New Roman', serif"
  mono:
    fontFamily: "'SFMono-Regular', Consolas, 'Liberation Mono', monospace"
rounded:
  DEFAULT: "0px"
spacing:
  space-1: "0.25rem"
  space-2: "0.5rem"
  space-3: "0.75rem"
  space-4: "1rem"
  space-5: "1.25rem"
  space-6: "1.5rem"
  space-8: "2rem"
  space-10: "2.5rem"
  space-12: "3rem"
  space-16: "4rem"
  touch-target: "2.75rem"
components:
  button: {}
  input: {}
  select: {}
  textarea: {}
  dialog: {}
  table: {}
  navigation: {}
---

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

## Overview

### Creative North Star

A working paper ledger: ruled registers, an ink-and-copper mark, deliberate serif headings, and precise operational annotations make configuration state legible without marketing ornament.

### Product context and register

- **Audience and primary job:** Internal administrators and project members review configuration, revisions, membership, machine access, and operational status.
- **Target market(s) and evidence:** No geographic market is asserted. The approved localization design specifies English and Simplified Chinese UI support only.
- **Locale(s) and language policy:** `en-US` and `zh-CN`; a valid explicit browser-local preference takes precedence, then browser language, then `en-US`. Product data, API values, and user-entered content remain untranslated.
- **Usage scene:** Desktop-first administrative work with dense registers and responsive access to the same controls.
- **Register:** Product/admin across login and authenticated routes.
- **Memorable signature:** The small copper square and ruled ledger details carry the visual identity while working controls stay plain.
- **Restraint:** Use familiar native semantics, strong type hierarchy, and borders for clarity; avoid decorative gradients, rounded cards, and promotional hero treatment.
- **Anti-references:** Consumer dashboards, glossy SaaS gradients, and card-heavy marketing layouts would obscure the register vocabulary and dense operational task focus.
- **Token ownership/runtime mapping:** Existing runtime canonical (Model B): `web/src/styles.css` defines CSS custom properties; React classes consume them. `DESIGN.md` mirrors values and intent only. Design lint is the documentation gate; no generated token artifact exists.

## Colors

Dark ink (`ink`) carries primary text and primary action surfaces. Warm `canvas` and `surface` layers create document-like depth; `surface-quiet` separates navigation and supporting regions. `line` and `line-strong` make state boundaries explicit. Copper provides the brand mark and restrained active accent; `error` is reserved for recoverable failures and destructive intent. `focus` supplies the visible global focus outline. The present runtime has one light theme and no separate dark/high-contrast token set; forced-colors remains browser-operable.

## Typography

The sans stack is the control and body face. Its platform fallbacks carry the active browser’s CJK fallback for `zh-CN`; no remote font is loaded. Georgia / Times New Roman forms the ledger-like display hierarchy for page and section headings, while the mono stack is reserved for configuration keys and technical identifiers. Compact uppercase labels use tracking for metadata; prose and controls use sentence case. Mixed-script text must wrap safely and retain enough line height for Chinese glyphs.

## Layout

The application uses a responsive document frame: a wrapping header, horizontally scrollable primary navigation when needed, and a padded content canvas. Page widths are bounded by content type (`27rem` login form, `50rem` headings, `76rem` resource registers), with CSS container queries handling local density. The runtime spacing sequence is exactly the `--space-*` scale in `styles.css`; touch targets are at least `--touch-target` (`2.75rem`). Loading, error, and dialog regions reserve geometry to avoid moving controls.

## Elevation & Depth

Hierarchy comes from warm surface changes, ruled borders, and constrained overlays rather than shadows. Dialogs use the `dialog-backdrop` ink wash and a bordered surface panel. Flat document surfaces remain the default; ordinary cards and controls do not gain elevation.

## Shapes

The default radius is zero. Inputs, buttons, native selects, tables, and dialog panels use squared corners with explicit one-pixel borders. The copper brand mark is a small square. Shape is not used to signal semantic priority; tone, label, border, and placement do that work.

## Components

### Foundational visual states

Controls use the canonical ink/surface/line tokens. Enabled buttons and links expose pointer, hover, active, disabled, and `:focus-visible` states. Inputs and selects use stronger borders and copper inset emphasis on focus. Busy controls retain their dimensions; inline status and error text reserve space. Existing loading is text/status based, not skeleton based.

### Buttons and actions

Primary actions are dark-ink, left-aligned ledger actions with a copper rule. Secondary, text, quiet, and navigation actions are bordered or understated. Destructive actions use `error` and remain separated from safe actions. All actions preserve the minimum touch target and use text labels where the action matters.

### Navigation and data display

The application shell contains the brand lockup, session controls, a responsive primary navigation region, and a focusable content landmark. Registers use semantic lists and tables, ruled rows, monospaced identifiers, and explicit empty/loading/error states. Long data wraps or scrolls horizontally rather than disappearing.

### Forms and overlays

Fields are labelled native controls with compact uppercase metadata labels, strong borders, inline error text, and retained drafts. Native selects are canonical because operating-system-owned popup geometry is accepted. The `datetime-local` control is likewise platform-owned; its browser/OS locale, popup, geometry, and behavior are accepted. `ModalDialog` owns app dialogs with focus placement, Escape behavior, trapping, and focus restoration; product flows do not use browser dialogs.

### Iconography

The current interface is text-led and does not establish a third-party icon family. The brand mark is decorative. Where an icon-only control is added later, it must have a localized accessible name and follow the established stroke, sizing, and focus rules rather than introduce a parallel vocabulary.

### Motion

Motion is short (`120ms`–`140ms`) and stateful: input border/background transitions, button press displacement, and skip-link reveal. It must remain interruptible and respect existing reduced-motion support when introduced; language changes do not animate or reset page state.

### Content and data visualization

Use precise, plain operational language: name the record, state, action, and recovery path. Product names and domain data stay verbatim. Dates and times use the active UI locale when localized formatting is implemented in later work; this foundation only establishes the locale boundary. No charts are currently part of the visual system.

## Do's and Don'ts

- **Do:** Keep the ruled ledger, high-contrast type hierarchy, and square control grammar visible in every shared surface.
- **Do:** Reuse `web/src/styles.css` variables and existing shared React primitives; treat runtime CSS as canonical.
- **Don't:** Add a second theme, remote font, or unrelated visual accent for localization.
- **Don't:** Treat platform-owned select or `datetime-local` popup geometry as authored UI, or hide a workflow-critical localized action through clipping.
