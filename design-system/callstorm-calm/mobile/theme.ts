// Generated. Logical layout units; native system font; allow text scaling.
export const colors = {
  "canvas": "#f3f2ee",
  "surface": "#ffffff",
  "sidebar": "#eeeee8",
  "surface-detail": "#faf9f5",
  "ink": "#20211e",
  "text-secondary": "#666b5d",
  "text-caption": "#697060",
  "muted-original": "#74766d",
  "border": "#e4e4db",
  "border-strong": "#828876",
  "olive": "#657f3b",
  "lime": "#e2eccb",
  "blue": "#c6dcfa",
  "lavender": "#dbc5e2",
  "peach": "#f7d1a8",
  "warm": "#fae9d7",
  "action": "#343e29",
  "action-hover": "#4c5b3b",
  "on-action": "#ffffff",
  "focus": "#657f3b",
  "selection": "#dfe8cb",
  "selection-text": "#34442c",
  "summary": "#e9edde",
  "summary-border": "#e0e5d5",
  "success-surface": "#e7edda",
  "success-text": "#526a35",
  "warning-surface": "#f3e7d8",
  "warning-text": "#624d34",
  "danger-surface": "#f4e1db",
  "danger-text": "#8a3e32",
  "info-surface": "#e8eff7",
  "info-text": "#3e5877",
  "chart-primary": "#607da2",
  "chart-comparison": "#8a6698",
  "chart-efficiency": "#657f3b",
  "chart-target": "#897047",
  "disabled-surface": "#e8e9e0",
  "disabled-text": "#747a6c"
} as const;
export const spacing = {
  "0": 0,
  "4": 4,
  "8": 8,
  "12": 12,
  "16": 16,
  "20": 20,
  "24": 24,
  "32": 32,
  "40": 40,
  "48": 48,
  "64": 64
} as const;
export const radii = {
  "small": 4,
  "control": 6,
  "nested": 8,
  "card": 12,
  "dialog": 14,
  "pill": 999
} as const;
export const sizes = {
  "controlWeb": 44,
  "controlMobile": 48,
  "sidebar": 218,
  "compactRail": 76,
  "topbar": 72,
  "detailPanel": 280,
  "drawer": 520,
  "contentMax": 1510,
  "icon": 20
} as const;
export const typeStyles = {
  "page": {
    "fontSize": 33,
    "lineHeight": 39.6,
    "letterSpacing": -1.25,
    "fontWeight": "400"
  },
  "insight": {
    "fontSize": 25,
    "lineHeight": 30.0,
    "letterSpacing": -0.7,
    "fontWeight": "400"
  },
  "section": {
    "fontSize": 21,
    "lineHeight": 28.35,
    "letterSpacing": -0.5,
    "fontWeight": "400"
  },
  "card": {
    "fontSize": 20,
    "lineHeight": 27.0,
    "letterSpacing": -0.5,
    "fontWeight": "400"
  },
  "body": {
    "fontSize": 16,
    "lineHeight": 25.6,
    "letterSpacing": 0,
    "fontWeight": "400"
  },
  "label": {
    "fontSize": 14,
    "lineHeight": 19.6,
    "letterSpacing": 0,
    "fontWeight": "400"
  },
  "caption": {
    "fontSize": 12,
    "lineHeight": 18.0,
    "letterSpacing": 0,
    "fontWeight": "400"
  },
  "eyebrow": {
    "fontSize": 12,
    "lineHeight": 18.0,
    "letterSpacing": 1.5,
    "fontWeight": "400"
  },
  "metric": {
    "fontSize": 37,
    "lineHeight": 40.7,
    "letterSpacing": -1.0,
    "fontWeight": "400"
  }
} as const;
export const buttonStyles = { minHeight: sizes.controlMobile, borderRadius: radii.control, paddingHorizontal: spacing["16"], backgroundColor: colors.action, justifyContent: "center", alignItems: "center" } as const;
