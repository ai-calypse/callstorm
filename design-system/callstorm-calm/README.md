# Callstorm Calm · Design system v1.1

A portable design system extracted from your Callstorm dashboard: warm neutral surfaces, olive actions, pastel accents, regular-weight typography, and one focused task at a time.

This is your dashboard's reusable style, inspired by the references you supplied. It is not Coval's official design system or a copy of its source code.

## Start here

1. Open `preview.html` for the complete standalone visual reference. It contains 12 browsable sections, a View all mode, interactive component specimens, embedded desktop and mobile screens, token search, and resource downloads. It requires no neighboring files or network access.
2. Read `DESIGN_SYSTEM.md` for design rules and component specifications.
3. Give `APPLY_STYLE_PROMPT.md`, `tokens.json`, and the relevant platform files to your coding agent.
4. For web, load `web/theme.css`, then `web/components.css`. Add `cs-app` to your page's root element and use the opt-in `cs-*` classes.
5. For React Native, import `mobile/theme.ts`. For Flutter, use `mobile/callstorm_theme.dart`. Read `mobile/README.md` for adaptation details.

## File map

| File | Purpose |
| --- | --- |
| `tokens.json` | Authoritative, framework-neutral color, spacing, typography, radius, and motion values |
| `DESIGN_SYSTEM.md` | Foundations, components, data visualization, responsive behavior, states, and UX rules |
| `APPLY_STYLE_PROMPT.md` | Reusable instructions for applying the style to another product |
| `preview.html` | Complete self-contained visual guide with 12 sections, embedded resources, and working examples |
| `build_preview.py` and `preview-src/` | Regenerate the self-contained visual guide |
| `reference/pastel-options.json` | Eight optional pastel families from the supplied reference; separate from core tokens |
| `reference/dashboard/` | Original dashboard source embedded in the desktop specimen |
| `web/theme.css` | Generated CSS custom properties |
| `web/components.css` | Opt-in reusable web component styling |
| `mobile/theme.ts` | Generated TypeScript tokens and React Native-compatible text/button styles |
| `mobile/callstorm_theme.dart` | Generated Flutter colors, spacing, typography, and light ThemeData adapter |
| `mobile/README.md` | Native mobile usage and responsive adaptation |
| `build_tokens.py` | Regenerates CSS, TypeScript, and Dart adapters using Python's standard library |
| `reference/original-dashboard.css` | Unmodified source stylesheet for exact historical reference |
| `VALIDATION.md` | What was checked and what still requires app-level testing |

## Changing the system

Edit `tokens.json`, then run:

```sh
python build_tokens.py
```

This overwrites `web/theme.css`, `mobile/theme.ts`, and `mobile/callstorm_theme.dart`. Keep app-specific overrides outside those generated files. `components.css` and the layout examples remain hand-authored and must be updated manually if a token's meaning changes.

The JSON uses a documented custom format. It is not automatically importable as Figma Variables or a guaranteed design-token interchange format. Use it as a source of truth for adapters; no design tool subscription is required.

## Scope

A light-theme foundation and component styling kit, not a full framework component library. It does not include a backend, production data, authentication, a Figma file, or a dark theme. Native adapters preserve the visual direction while using native fonts and platform interactions. The source dashboard's data and conversations are illustrative.

## Rebuild the visual guide

After updating tokens or handoff resources:

```sh
python build_tokens.py
python build_preview.py
```

The preview embeds the theme, component styles, catalog CSS and JavaScript, source dashboard, tokens, and handoff resources. Its download buttons generate local files from the embedded contents. Edit `preview-src/gallery.css`, `preview-src/gallery.js`, and `build_preview.py` to update the catalog.

The visual guide version is 1.1. Core design tokens remain at 1.0 because this update expands the visual documentation rather than changing the shared theme. Optional reference pastel families are clearly marked and are not automatically included in generated platform adapters.
