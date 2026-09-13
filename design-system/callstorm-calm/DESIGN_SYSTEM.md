# Callstorm Calm

Version 1.0 · extracted from the Callstorm evaluation dashboard

## 1. The design idea

Make complex work feel understandable. The interface should help someone choose a question, understand the answer, and inspect the evidence without losing their place.

The visual signature has five parts:

- A warm off-white canvas with a slightly darker navigation surface.
- White work areas, subtle borders, and almost no elevation.
- Dark olive primary actions; pale blue, lavender, peach, and lime supporting accents.
- Regular-weight sans-serif headings with modest negative tracking; small monospace labels used sparingly.
- A compact summary followed by one focused work area. Detailed evidence opens on demand.

Retain those relationships across products. A shopping app does not need evaluation charts; a notes app does not need a KPI row. Reuse the hierarchy and visual language around the new product's actual task.

## 2. Provenance and deliberate normalization

The original dashboard used Arial, not a premium or proprietary typeface. Its original stylesheet is included verbatim in `reference/original-dashboard.css`.

`tokens.json` records exact source colors in `sourceExact`. A few repeated near-identical neutrals have been consolidated into semantic roles. The reusable kit also introduces darker secondary text, stronger control boundaries and chart strokes, larger controls, and consistent mobile type sizes. Those are export adaptations, not claims about the original implementation.

Original one-off spacing values such as 22, 25, 27, and 42 px are normalized to a small scale. The most recognizable geometry—218 px sidebar, 280 px insight column, 520 px drawer, 12 px cards—remains available.

The original dashboard is not a complete accessibility benchmark. Preserve its character while applying the stronger reusable roles and app-level checks in this kit.

## 3. Color foundations

| Role | Value | Use |
| --- | --- | --- |
| Canvas | `#F3F2EE` | Page background |
| Surface | `#FFFFFF` | Main work area, inputs, cards |
| Sidebar | `#EEEEE8` | Secondary navigation plane |
| Detail surface | `#FAF9F5` | Insight column or evidence panel |
| Primary text | `#20211E` | Headlines, main body, important values |
| Secondary text | `#666B5D` | Descriptions and supporting labels |
| Hairline border | `#E4E4DB` | Nonessential card separators |
| Strong border | `#828876` | Control edges that must remain perceptible |
| Primary action | `#343E29` | Main button or selected action |
| Primary hover | `#4C5B3B` | Hover state |
| Olive | `#657F3B` | Focus indicator, efficiency chart, emphasis |
| Lime | `#E2ECCB` | Small supporting fills |
| Selected surface | `#DFE8CB` | Active navigation item |
| Summary surface | `#E9EDDE` | Calm summary region |
| Powder blue | `#C6DCFA` | Informational fills or first chart category |
| Lavender | `#DBC5E2` | Comparison accents or second category |
| Peach | `#F7D1A8` | Attention accents or third category |
| Warm cream | `#FAE9D7` | Warm supporting surface |

Use the dominant neutral surface for most of a screen. A useful starting balance is roughly 80% neutral, 15% quiet tinted surfaces, and 5% strong emphasis. This is a composition guide, not a per-pixel requirement.

Pastels are fills, not small text colors. Pair them with `ink` or an appropriate dark semantic role. Never use a pale line alone to carry a critical chart distinction.

### Semantic states

| State | Surface | Foreground | Required companion |
| --- | --- | --- | --- |
| Success | `#E7EDDA` | `#526A35` | Label such as “Completed” |
| Warning | `#F3E7D8` | `#624D34` | Specific consequence or missed target |
| Error | `#F4E1DB` | `#8A3E32` | What failed and how to recover |
| Information | `#E8EFF7` | `#3E5877` | Useful contextual explanation |

Success, warning, and information derive from the source's existing direction. Error roles are an extension for reusable app states. A category's lavender or blue does not imply success or failure. Use status text and icons when conveying an outcome.

## 4. Typography

Web font stack: `Arial, Helvetica, sans-serif`. Metadata stack: `"Courier New", monospace`. Native mobile uses the platform's system sans-serif unless the product supplies an appropriately licensed font.

| Style | Size | Weight | Line height | Tracking |
| --- | --- | --- | --- | --- |
| Page heading | 33 | 400 | 1.2 | -1.25 |
| Insight takeaway | 25 | 400 | 1.2 | -0.7 |
| Section heading | 21 | 400 | 1.35 | -0.5 |
| Card heading | 20 | 400 | 1.35 | -0.5 |
| Body | 16 | 400 | 1.6 | 0 |
| Control / label | 14 | 400 | 1.4 | 0 |
| Secondary caption | 12 | 400 | 1.5 | 0 |
| Eyebrow | 12 | 400 | 1.5 | +1.5 |
| Metric | 37 | 400 | 1.1 | -1.0 |

Numbers are logical pixels before platform scaling. CSS adapters use rem. Use tabular numerals for changing metrics and aligned numeric columns. Reserve bold text for selected states or brief inline emphasis; do not make every card title semibold.

Eyebrows are optional orientation aids such as “EVALUATION” or “ORDER DETAILS.” They should never duplicate a nearby heading with different words. On small screens, page headings can reduce to 28 while body and controls retain their size. Respect operating-system text scaling.

## 5. Spacing, geometry, and depth

Spacing scale: `0, 4, 8, 12, 16, 20, 24, 32, 40, 48, 64`.

| Element | Default |
| --- | --- |
| Desktop page inset | 40 |
| Tablet inset | 24 |
| Mobile inset | 16 |
| Card internal padding | 24 desktop; 20 compact |
| Gap between related controls | 8–12 |
| Gap between sections | 32–40 |
| Badge radius | 4 |
| Button / input radius | 6 |
| Nested evidence block radius | 8 |
| Main card radius | 12 |
| Modal radius | 14 |
| Default border | 1 |
| Web control height | At least 44 |
| Mobile control height | At least 48 |

These touch-target values are this kit's design defaults. They are not a claim that a component automatically meets every accessibility requirement.

Use a single flat border for ordinary cards. Save shadows for overlays that physically cover content. Avoid glass effects, gradients, huge rounded cards, and floating shadows under every metric. Circular shapes are appropriate for avatars or icon-only controls, not the default for all buttons.

## 6. Composition and progressive disclosure

### The default working screen

1. Name the current object or task and show only essential context.
2. If a summary helps the decision, show up to three meaningful values or one compact status.
3. Offer two to four relevant questions or task categories.
4. Show one primary visualization, list, editor, or form in the work area.
5. Present a short interpretation and next action beside it on wide screens.
6. Open records, rationale, traces, or secondary settings in a drawer, sheet, or detail page.

The chart count is a default for overview density, not a ban on analytical comparison. If two charts are necessary to answer a single question, give them a shared heading and an explicit relationship. Do not add graphs simply because more metrics exist.

Do not force every page into the dashboard layout. Forms should use coherent field groups, message apps should center the conversation, and commerce screens should center products or checkout. The same surfaces, type, spacing, and disclosure rules remain applicable.

### Responsive strategy

| Width / environment | Behavior |
| --- | --- |
| Wide, at least 1100 | Optional 218 sidebar; work area plus 280 interpretation column |
| Compact, 760–1099 | Optional 76 icon rail with accessible names; fewer horizontal controls |
| Small, below 760 | Single content column; interpretation follows the work area |
| Native mobile | Use native navigation appropriate to the app; do not shrink the desktop sidebar into the screen |

These normalized breakpoints are recommendations. Original responsive breakpoints are preserved in the source snapshot. Let real content and text scaling determine when a layout changes.

For three to five stable destinations, a native bottom navigation bar can replace side navigation. For local content categories, use tabs or a segmented control. For a deep record, use a pushed screen or full-height sheet with clear dismissal. Always account for safe areas and the on-screen keyboard.

## 7. Component contracts

### Primary and secondary buttons

Primary: dark olive surface, white label, 6 radius, 14 label, at least 44 web / 48 native height. Use one obvious primary action per task area. Secondary: transparent surface, strong border, dark text. Tertiary: text action with a visible hover/focus affordance.

States: hover darkens the action surface; keyboard focus adds an offset olive ring; disabled uses a quiet neutral surface and actual disabled behavior; loading retains the label or replaces it with a precise progress label and prevents duplicate submission. Success should update the affected record, not merely flash a toast.

### Inputs

White surface, dark text, persistent external label, 6 radius, strong boundary, 16 input text, comfortable padding. Place a help message below the field only when needed. Error styling must include a specific inline message, `aria-invalid`, and an associated description on web. A placeholder is an example, not a label.

### Cards and summaries

White card, hairline border, 12 radius, 24 padding. A summary may use the green-tinted summary surface. Related values can be separated by thin vertical rules on desktop; stack or wrap them on small screens. Do not outline every short paragraph as a new card.

### Tabs

Low-emphasis labels on the neutral canvas; selected tab gets dark text and a thin olive underline. Optional pastel icon tile identifies the category. Preserve selected state, keyboard arrow navigation, accessible names, and panel association. Never reduce mobile tab labels to illegible sizes to make them fit.

### Insight panel

A white working area beside a quiet detail surface. The interpretation contains one answer, a supporting observation, and at most one suggested next action. It must change when the user changes the relevant selection.

### Drawers and sheets

Desktop reference width: 520. Include a clear heading, contextual identity, close control, focused evidence, and an optional collapsed definition section. On web, use an accessible dialog primitive or native dialog where appropriate. Manage focus, Escape, scroll, and focus restoration. On mobile, choose a native sheet or detail screen; do not constrain long evidence to a tiny modal.

### Lists and tables

Prefer thin row dividers and readable alignment over nested cards. Make the selected record clear with a tint and a label or control state. Align numbers consistently. A row must not imply it is clickable unless it opens or changes something. On small screens, prioritize fields in a record layout when a wide table would become unreadable.

## 8. Charts that answer a question

Every analytical visualization has five pieces:

1. Question: “At what load does response time exceed our target?”
2. Measure and unit: “P95 response time · seconds · lower is better.”
3. Direct answer: “The first tested breach occurs at 40 concurrent calls.”
4. Evidence: labeled axes, sample sizes, reference period, target, and relevant annotations.
5. Follow-up: a way to inspect the selected record or underlying values.

Use a bar chart for category comparisons, line chart for an ordered progression, and a segmented bar for an additive breakdown. Do not sum percentiles as if they were additive stage times. Do not give a categorical x-axis continuous spacing unless that encoding is intentional and clear.

Default chart roles: primary stroke `#607DA2`, comparison stroke `#8A6698`, efficiency stroke `#657F3B`, target stroke `#897047`. Pastel palette colors can fill bars or confidence regions; pair pale bars with a stronger outline if their boundary communicates data. Also distinguish series with dashes, markers, or direct labels.

Keep titles and labels readable at their rendered size. Avoid shrinking an entire SVG and its text to fit mobile. Reduce tick count, reflow labels, or switch to an accessible list of values. Provide a keyboard-accessible control or data table when chart points can be inspected; a mouse-only tooltip is insufficient.

Always label synthetic data. Separate measured evidence from interpretation. Display a missing value as unavailable, not as zero. A capacity statement derived from latency alone is not proof of overall release readiness.

## 9. Motion and full state coverage

Default color/hover transitions: 150 ms. Panel transitions: 200 ms, ease-out. Honor reduced-motion settings. Use motion to preserve orientation, not to animate every number and chart whenever a user changes a tab.

| State | Treatment |
| --- | --- |
| Loading | Preserve layout; show localized progress; prevent duplicate actions |
| Empty | Name what is missing and show a relevant action, if one exists |
| No filter results | Keep filters visible and offer a clear reset |
| Error | State what failed, retain the user's input, and offer a real retry or recovery |
| Disabled | Explain a non-obvious prerequisite nearby; implement disabled behavior |
| Success | Update the affected content and announce the result where useful |
| Stale data | Show when it was updated and whether refresh is available |

## 10. Reuse across products

| Product | Primary work area | Quiet secondary detail |
| --- | --- | --- |
| Finance tracker | Spending category list or one trend | Selected transaction explanation |
| Learning app | Current lesson or exercise | Hint, example, or progress context |
| Project manager | Active task list | Selected task details |
| Commerce app | Product selection or checkout step | Delivery / price explanation |
| Health habit tracker | Today's actions | History for the selected habit |
| Developer tool | Selected run, trace, or editor | Diagnostic finding and next action |

Change the domain language, information architecture, and interaction model. Retain the token roles, regular typography, quiet surfaces, and focused disclosure. Do not transplant Callstorm branding or fictional evaluation metrics into unrelated apps.

## 11. Completion checks

- Does the first screen expose the actual task?
- Is the dominant surface warm neutral, with olive reserved for meaningful emphasis?
- Can the reader see the main answer without parsing several charts?
- Do visible controls work, including keyboard and touch interaction?
- Are labels, values, and status messages readable without color alone?
- Does the layout survive enlarged text, narrow screens, and long translated labels?
- Are loading, empty, error, and success states represented where the task needs them?
- Are data, selection, interpretation, and evidence consistent?
- Are exact source values and new adaptations distinguishable?

The kit includes contrast calculations for named color pairs, not a certification of an entire app. Validate the finished application with its actual components, content, devices, and assistive technologies.
