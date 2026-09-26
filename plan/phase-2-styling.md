# Phase 2 Styling Plan — Soft-Brutalist Family Gallery

## Goal and boundaries

Restyle the existing family gallery and admin screens into a consistent, accessible soft-brutalist interface. This phase changes presentation only: preserve the current Go `html/template` rendering, routes, form methods/actions, field names, CSRF fields, session separation, photo authorization, upload validation, and delete confirmation. Do not add product features, change server behavior, add dependencies, or expose hidden photos. Do not implement image optimization, search, albums, or other PRD out-of-scope work.

The PRD deliberately deferred final styling and asks for flexible, minimal HTML styling. This plan is a follow-up visual phase, not a change to MVP behavior or security requirements. Continue using the existing system sans-serif stack and inline/browser-native interactions where practical; no icon or font dependency is needed.

## Current UI and routes

| Route/state | Current template and behavior | Styling focus |
| --- | --- | --- |
| `GET /` without family session | `templates/gallery.html`: heading plus a fixed password gate, explanatory copy, password form posting to `/login`. | Treat the gate as a focused, centered access card over a calm canvas; retain the family-gallery context and usable narrow-screen layout. |
| `GET /` with family session | Same template: sign-out form posting to `/logout`, visible-photo grid, labels, or “No photos have been shared yet.” | Make the heading/sign-out hierarchy clear, cards consistent, images contained, and empty state intentional. Preserve `/media/{id}` links and current new-tab behavior. |
| `GET /admin` without admin session | `templates/admin-login.html`: username/password form posting to `/admin/login` and link to `/`. | Use the shared form/card primitives while keeping admin sign-in visibly separate from family access. |
| `GET /admin` with admin session | `templates/admin.html`: sign-out, upload form posting to `/admin/photos`, and photo cards with label, visibility, and delete forms. | Make upload the first clear task; distinguish hidden status and destructive action; keep each photo's actions grouped and legible at mobile widths. |
| Admin delete confirmation | `static/admin.js`: native `window.confirm` for forms marked `data-confirm`. | Retain native confirmation and its existing hook; ensure the delete control is clearly destructive without implying a custom dialog exists. |
| Form errors | Login/upload/label errors currently use plain HTTP error responses, not template-level inline messages. | Do not invent a new error flow in a styling-only phase. Keep native required/input behavior and do not suggest errors are presented inline. A designed error state requires a separate behavior change. |

Existing styling is embedded separately in the three templates. `static/` currently contains only `admin.js`; there is no shared stylesheet, component library, icon package, or existing design contract. A key functional detail is that family photo links use `target="_blank"`; do not silently replace this with a modal/lightbox as part of styling.

## Design contract

Before template styling work, create a concise root `DESIGN.md` from this contract so later UI work has a durable reference. Keep it aligned with this plan; it does not authorize broader redesign or behavior changes.

### Paradigm and rationale

Use functional soft brutalism: strong ink outlines and clear alignment express structure; warm neutral surfaces and restrained pastel accents soften the family-focused photo product. Keep the photo content and practical admin actions dominant. Avoid ornamental hero sections, gradients, broad shadows, and decorative asymmetry.

### Tokens

Define the shared tokens centrally in a new `static/site.css` under `:root`, then use them across all templates. These are the starting values and should only be adjusted after contrast verification:

```css
:root {
  --color-canvas: #fbf8f1;
  --color-surface: #fffefa;
  --color-surface-muted: #f1ede5;
  --color-ink: #292722;
  --color-muted: #625f57;
  --color-subtle: #77736b;
  --color-line: #c9c2b5;
  --color-primary: #fa8f78;
  --color-primary-dark: #713d32;
  --color-primary-soft: #ffd8cb;
  --color-lavender: #d9d2ff;
  --color-mint: #ccebdc;
  --color-butter: #f6df8b;
  --color-danger: #a53b48;
  --shadow-lift: 3px 3px 0 rgba(41, 39, 34, .16);
  --ease-tactile: 140ms cubic-bezier(.2, .8, .2, 1);
}
```

Use the neutral canvas and surfaces for most pixels. Use coral for primary actions, secondary pastel fills sparingly for distinct regions/status, ink for text and key outlines, and muted line color for quiet separators. Verify actual text/background pairs to WCAG 2.2 AA: at least 4.5:1 for normal text and 3:1 for large text and essential control boundaries/focus indicators. In particular, do not assume the lighter pastel fills or `--color-danger` are safe text colors without testing; darken text/accent shades as needed while retaining the palette relationship.

### Type, spacing, geometry, and layout

- Use the existing `system-ui, sans-serif` stack; no remote fonts. Headings and controls use sturdy, readable weights; body copy remains compact and legible. Use monospace only if compact technical metadata is later shown (not required here).
- Use a 4px spacing rhythm, with common gaps of 8, 12, 16, 24, and 32px.
- Use a consistent radius scale: 8px for small controls, 12–14px for fields/cards, and up to 20px for the main access card. Avoid pill-shaped controls except where a true compact status chip is useful.
- Use 2px ink outlines for primary controls, selected/high-priority boundaries, and key cards; 1px muted borders for separators. Use the crisp offset shadow selectively (primary buttons and the gate card), not on every element.
- Keep content at a readable maximum width. Gallery cards use an auto-fitting grid with a mobile-safe minimum; admin photo cards use a denser grid only where the available width supports it. Preserve image proportions with `object-fit: contain` and do not crop family photographs to create uniformity.

### Shared primitives and states

- Shared page shell/header, heading hierarchy, card, form field, button, status label, and empty-state styles live in `static/site.css` and are reused by all three templates.
- Primary buttons: pastel fill, ink text, 2px ink outline, medium radius, and small offset shadow. Hover lifts slightly and strengthens the crisp shadow; active presses down and reduces it. Disabled controls are visibly disabled if any are present; do not add disabled behavior.
- Secondary controls use neutral or muted pastel fill. Delete is clearly destructive by text and a distinct danger treatment, not color alone.
- Inputs have a neutral surface, clear border, comfortable padding, and a high-visibility focus ring. Associate every input with its existing visible label.
- Photo cards group image, optional label/hidden state, and their existing actions. A hidden photo's state remains explicitly labeled “Hidden” (not color-only); visibility action text remains “Show to family” or “Hide from family.”
- Provide visible hover, active, focus-visible, and disabled states where relevant. Keep transitions short and respect `prefers-reduced-motion` by removing nonessential movement/transition.
- Use no new icon dependency. Existing labels provide meaning; if a small decorative icon is used, keep it consistent and hide it from assistive technology when redundant.

## Files and implementation sequence

1. **Design record and shared CSS:** add root `DESIGN.md` based on this contract; add `static/site.css` for tokens, reset/base typography, page shell, shared form/button/card states, focus treatment, and responsive rules. The server already embeds and serves `static/*`, so no Go asset wiring change should be needed.
2. **`templates/gallery.html`:** remove the page-specific inline `<style>` and link `/static/site.css`. Style both authenticated and password-gate states. Preserve `GET /`, form actions `/login` and `/logout`, hidden CSRF input, IDs/names/autocomplete/required attributes, photo labels/alt text, photo URLs, and existing new-tab link semantics. Keep all page content in English, matching current templates and repository language rules.
3. **`templates/admin-login.html`:** use the same stylesheet and form/card primitives. Preserve `/admin/login`, `/` link, CSRF field, labels, usernames/password field names and autocomplete, and native required validation. Make separation from family access clear through heading/copy and layout, not a new access mechanism.
4. **`templates/admin.html`:** use the stylesheet for header, upload panel, photo grid/cards, status, fields, and action hierarchy. Preserve `/admin/logout`, `/admin/photos`, `/admin/photos/{id}/label`, `/admin/photos/{id}/visibility`, and `/admin/photos/{id}/delete`; retain all method/enctype/accept/maxlength/CSRF attributes, `data-confirm`, image URL/alt text, button text, and empty state. Do not reorder/remove functional controls in a way that breaks association with their photo.
5. **`static/admin.js`:** no behavior change is planned. Keep the native destructive confirmation functional; only touch this file if a harmless presentation-related compatibility issue is discovered and document it separately.

Do not modify `main.go`, route handlers, authentication or photo-serving logic for this styling phase. In particular, never weaken server-side checks on `/media/{id}` or imply that hidden media can be fetched by family users.

## Responsive requirements

- At 320px viewport width, no horizontal page scrolling; forms, gate card, card actions, and labels fit within the viewport with comfortable side padding.
- Use fluid page padding and a centered max-width shell; collapse header content cleanly when title and sign-out form cannot fit on one line.
- Gallery/admin grids use `auto-fit`/`auto-fill` with a safe minimum width and collapse to one column on narrow screens. Avoid fixed card widths that overflow.
- Inputs and buttons are full-width where that improves mobile usability (notably login and upload forms); admin card actions remain visually grouped and easy to tap. Target at least 44×44 CSS px for primary touch controls where feasible.
- Password gate remains centered without clipping on short viewports: allow vertical scrolling, constrain its width, and avoid relying on a fixed height. Do not obscure the page with decorative overlays beyond the existing unauthenticated gate.
- Images remain contained, scale to card width, and do not create horizontal overflow. Ensure labels and long user-provided text wrap safely.

## Accessibility requirements

- Preserve semantic landmarks (`header`, `main`, sections/headings), existing explicit labels, image alt text, and descriptive action text. Use headings in logical order; do not use visual styling as a replacement for semantic structure.
- Maintain keyboard access to every form control and link, with a clear `:focus-visible` indicator that is not clipped or color-only. Keep native form validation and native delete confirmation accessible.
- Maintain the contrast targets in the design contract for text, outlines, focus indicators, and state labels. Pair color state with text (“Hidden”) and existing action wording.
- Avoid motion by default or keep it subtle; honor `prefers-reduced-motion: reduce`.
- Ensure CSS does not visually hide labels, focus, status, or empty-state copy. Keep zoom/reflow usable at 200% and at narrow widths.

## Visual verification and checks

- Run `go test ./...` after changes to verify templates still parse and existing behavior remains covered; run `gofmt` only if Go files were changed (they should not be).
- Run the app with local development configuration and inspect `/` in both unauthenticated and family-authenticated states, `/admin` before and after admin sign-in, plus empty and populated gallery/admin data if available.
- Inspect representative viewport sizes: 320px narrow mobile, ~390px mobile, tablet (~768px), and desktop (~1280px). Check grid transitions, headers, upload/form layout, image containment, text wrapping, and no horizontal overflow.
- Use keyboard-only navigation to exercise login, gallery sign-out, upload controls, label save, visibility toggle, and delete confirmation. Check focus visibility, native validation, and confirmation cancellation/acceptance without altering route behavior.
- Check browser console/network for missing CSS/assets and ensure photo/media access behavior remains server-controlled; use existing automated security/functionality tests for hidden-photo and separate-session invariants.
- Verify color contrast for actual text/control pairs rather than relying on token names. If browser tooling is available, capture screenshots of all four primary route states for review.

## Acceptance criteria

1. All three current templates use one shared tokenized stylesheet and have no duplicated page-level style blocks; a root `DESIGN.md` records the design contract.
2. Family gate, authenticated gallery, admin login, and admin dashboard have a consistent soft-brutalist visual language: neutral canvas, restrained pastel accents, crisp ink outlines, medium rounding, and selective tactile shadow.
3. Existing routes, form methods/actions, field names, CSRF inputs, validation attributes, photo URLs, labels, empty states, admin operations, and browser delete confirmation remain functional and unchanged in meaning.
4. Family photo links retain current new-tab behavior; no lightbox, search, album feature, or new backend interaction is introduced.
5. No Go route, session, authorization, or media-serving changes are made; hidden-photo access and separation of family/admin sessions remain protected by existing server logic and tests.
6. Layout remains usable without horizontal overflow at 320px, adapts through tablet/desktop widths, preserves image proportions, and keeps controls comfortably operable by touch and keyboard.
7. Text and essential UI boundaries meet WCAG AA contrast targets; visible focus, semantic labels/landmarks, textual state cues, native validation/confirmation, and reduced-motion support are present.
8. `go test ./...` passes, and visual verification covers authenticated/unauthenticated states and empty/populated states when sample data permits.
