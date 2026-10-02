---
name: stixor-design
description: Use this skill to generate well-branded interfaces and assets for Stixor Technologies, either for production or throwaway prototypes/mocks/etc. Contains essential design guidelines, colors, type, fonts, assets, and UI kit components for prototyping.
user-invocable: true
---

Read the README.md file within this skill, and explore the other available files.

If creating visual artifacts (slides, mocks, throwaway prototypes, etc), copy assets out and create static HTML files for the user to view. If working on production code, you can copy assets and read the rules here to become an expert in designing with this brand.

If the user invokes this skill without any other guidance, ask them what they want to build or design, ask some questions, and act as an expert designer who outputs HTML artifacts _or_ production code, depending on the need.

## Quick reference

- Palette: Charcoal `#2B2B2B` (primary), Warm Orange `#E97B50` (accent — **see orange-usage rule below**), Light Ivory `#F5F5F3` (background).
- Type: **Helvetica Neue** for display **and body** (the primary typeface). **DM Mono** for the utility layer — tables, taglines, labels, metadata, section numbers. **No Inter.**
- Signature elements: **2 px orange rule** as section divider; **hex / honeycomb** motif at low opacity behind hero blocks; large **section numbers** in orange (01, 02, 03 …).
- Voice: "Engineered Clarity" — confident, direct, technical-but-human, measurably precise. No hedging, no marketing fluff, no emoji, no exclamation points outside error UI.

## ⚠️ Orange usage rule

Orange is an **accent**, not a brand fill. Treat it like a highlighter — one or two strokes per surface.

**Yes:** logo mark · section numbers (01, 02 …) · the 2 px orange rule · single-word emphasis in a headline · eyebrow labels · hover/focus states · ≤24% image overlays.

**Never:** background fills · large solid rectangles or banners · body text · gradients · default link color · two adjacent UI elements at once. See README for the full rule.

## Files

- `colors_and_type.css` — drop into the `<head>` of any artifact for full token access.
- `assets/` — logos, hex motif, photography.
- `preview/` — small reference cards.
- `slides/index.html` — six-slide deck templates (title / section / pillars / stat / services / closing).
- `ui_kits/marketing-site/` — homepage components.
- `ICONOGRAPHY.md` — icon system rules (Lucide via CDN, hex motif, no emoji).
- `README.md` — full brand spec.
