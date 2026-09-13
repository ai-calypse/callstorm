# Validation record

## Checks performed

- Parsed the token source and regenerated all three platform adapters.
- Checked source-exact colors against the original dashboard stylesheet.
- Verified the original stylesheet snapshot byte-for-byte.
- Calculated contrast for the named foreground/background pairs below.
- Checked local HTML/CSS/JavaScript references and JavaScript syntax.
- Checked all 12 visual sections, embedded handoff-resource consistency, unique IDs, and absence of external assets.
- Checked control references and resource-download mappings in the standalone preview.
- Verified the final ZIP can be extracted without corruption.

## Foreground/background contrast

Values are computed from sRGB color values. These checks cover only the listed pairs, not full screens or application accessibility.

| Foreground | Background | Contrast ratio |
| --- | --- | --- |
| `ink` | `canvas` | 14.45:1 |
| `ink` | `surface` | 16.18:1 |
| `text-secondary` | `canvas` | 4.90:1 |
| `text-caption` | `surface-detail` | 4.88:1 |
| `on-action` | `action` | 11.25:1 |
| `on-action` | `action-hover` | 7.33:1 |
| `selection-text` | `selection` | 8.23:1 |
| `success-text` | `success-surface` | 5.05:1 |
| `warning-text` | `warning-surface` | 6.55:1 |
| `danger-text` | `danger-surface` | 5.91:1 |
| `info-text` | `info-surface` | 6.32:1 |

## Chart/control contrast on white

| Role | Contrast ratio |
| --- | --- |
| `chart-primary` | 4.24:1 |
| `chart-comparison` | 4.74:1 |
| `chart-efficiency` | 4.51:1 |
| `chart-target` | 4.70:1 |
| `border-strong` | 3.66:1 |
| `focus` | 4.51:1 |

## Limits

- Live browser inspection was attempted for the expanded preview. The local server was unreachable from the cloud browser, and direct local-file navigation was blocked by browser URL policy. No rendered-browser or assistive-technology audit is claimed.
- The Flutter adapter and React Native example were not compiled or tested on a device. Check them against the destination app’s SDK and component stack.
- WebMCP is not required by this design kit.
- Fonts, text scaling, localization, platform components, chart labeling, and real application states still need app-level validation.
- The original source snapshot includes the original compact text and low-emphasis colors; it is a provenance reference, not the recommended component stylesheet.
