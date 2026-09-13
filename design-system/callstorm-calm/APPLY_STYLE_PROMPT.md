# Apply Callstorm Calm to a new product

Copy the prompt below and attach this design-system folder, or at least `DESIGN_SYSTEM.md`, `tokens.json`, and the relevant platform adapter. Screenshots alone do not preserve exact values.

---

Build or restyle the following product using the attached **Callstorm Calm v1.0 design system**.

PROJECT
- Product and primary user: [describe]
- Most important task: [describe what the user should accomplish]
- Platform: [responsive web / iOS / Android / Flutter / React Native]
- Existing stack or repository: [describe or attach]
- Screens and capabilities in scope: [list]
- Existing brand/content to preserve: [list]
- Real data or API contracts: [attach, or explicitly request demo data]

Treat `tokens.json` as the token source of truth and `DESIGN_SYSTEM.md` as the behavioral and component guide. Use `reference/original-dashboard.css` only to resolve historical visual details. It is not the default reusable stylesheet.

VISUAL DIRECTION
- Use a warm off-white canvas, white working surfaces, dark olive primary actions, thin neutral dividers, and restrained powder blue, lavender, peach, and lime accents.
- Preserve semantic roles: pastels are supporting fills, not small text or essential chart outlines. Use the darker foreground and chart roles defined by the kit.
- Use regular-weight sans-serif headings, modest negative letter spacing, readable body text, and sparse monospace metadata. Use the supplied type scale rather than inventing another one.
- Use the supplied spacing and radius scales. Prefer flat bordered surfaces; reserve shadows for overlays. Keep decoration secondary to the task.
- Preserve any existing product identity that I explicitly asked to keep. Do not copy the Callstorm name, evaluation content, or its exact page structure into an unrelated product.

UX REQUIREMENTS
- Put the primary activity in the first useful viewport.
- Show only the information needed for the current decision. Use one focused work area and reveal secondary evidence or settings on demand.
- For analytical screens, default to one primary chart per selected question. Give each chart a plain-language question, measure and unit, direct takeaway, comparison or target where relevant, and an evidence path.
- For non-analytical screens, apply the same hierarchy to the actual task: a form, conversation, list, editor, product, or lesson. Do not add KPIs or charts just to imitate the reference.
- Keep selection, summary, detail, and interpretation synchronized. All visible controls must have real behavior.
- Include appropriate loading, empty, error, disabled, and success states. Preserve input when recovering from errors.

IMPLEMENTATION
- Reuse the existing app architecture and accessible component primitives. Apply the tokens through its theme layer rather than replacing the stack unnecessarily.
- For web, integrate `web/theme.css` and the opt-in patterns in `web/components.css`, or map the same tokens into the existing styling system.
- For React Native, use `mobile/theme.ts` as a foundation. For Flutter, use `mobile/callstorm_theme.dart` and the native guidance. These are starting adapters, not complete app component libraries.
- Adapt navigation and overlays to the platform. Preserve safe areas, text scaling, keyboard interaction, focus handling, and comfortable touch targets.
- Do not shrink desktop layouts and chart text to force them onto mobile. Reflow, reduce secondary density, and use native navigation patterns.
- Keep shared tokens centralized. Put project-specific extensions in a separate theme extension and explain the reason for each new role.
- Use real content or explicitly labeled sample data. Do not invent measured performance, customer counts, or test results.
- Do not add a dark mode unless it is requested; this kit defines a light theme.

DELIVER
Implement the requested screens and interactions. Provide the resulting code or working app, a brief map of token integration, and the checks you actually performed. Clearly identify any untested native adapters or unavailable runtime checks. Keep reasonable design decisions within the supplied system; ask only when missing information would fundamentally alter the product.

---

For an existing app, replace the first sentence with: “Restyle the existing app using Callstorm Calm while preserving its current behavior, routes, data contracts, and explicitly retained branding.”
