// Theme presets, one per Facet style (styles/<id>.yaml). A composition picks one
// with `theme`; `themeConfig` overrides individual fields. An unknown theme id is
// an error: it would otherwise render a video in a look nobody chose.

export interface ThemeConfig {
  primaryColor: string;
  accentColor: string;
  backgroundColor: string;
  surfaceColor: string;
  textColor: string;
  mutedTextColor: string;
  headingFont: string;
  bodyFont: string;
  monoFont: string;
  chartColors: string[];
  springConfig: { damping: number; stiffness: number; mass: number };
  transitionDuration: number;
  captionHighlightColor: string;
  captionBackgroundColor: string;
}

export const THEMES: Record<string, ThemeConfig> = {
  "clean-professional": {
    primaryColor: "#2563EB",
    accentColor: "#F59E0B",
    backgroundColor: "#FFFFFF",
    surfaceColor: "#F9FAFB",
    textColor: "#1F2937",
    mutedTextColor: "#6B7280",
    headingFont: "Inter",
    bodyFont: "Inter",
    monoFont: "JetBrains Mono",
    chartColors: ["#2563EB", "#F59E0B", "#10B981", "#8B5CF6", "#EC4899", "#06B6D4"],
    springConfig: { damping: 22, stiffness: 120, mass: 1 },
    transitionDuration: 0.4,
    captionHighlightColor: "#2563EB",
    captionBackgroundColor: "rgba(255, 255, 255, 0.85)",
  },
  "flat-motion-graphics": {
    primaryColor: "#7C3AED",
    accentColor: "#EC4899",
    backgroundColor: "#0F172A",
    surfaceColor: "#1E293B",
    textColor: "#F8FAFC",
    mutedTextColor: "#94A3B8",
    headingFont: "Space Grotesk",
    bodyFont: "Space Grotesk",
    monoFont: "JetBrains Mono",
    chartColors: ["#7C3AED", "#EC4899", "#06B6D4", "#F59E0B", "#10B981", "#EF4444"],
    springConfig: { damping: 12, stiffness: 80, mass: 1 },
    transitionDuration: 0.3,
    captionHighlightColor: "#22D3EE",
    captionBackgroundColor: "rgba(15, 23, 42, 0.75)",
  },
  "minimalist-diagram": {
    primaryColor: "#1A1A2E",
    accentColor: "#E94560",
    backgroundColor: "#FAFAFA",
    surfaceColor: "#FFFFFF",
    textColor: "#1A1A2E",
    mutedTextColor: "#6B7280",
    headingFont: "Inter",
    bodyFont: "Inter",
    monoFont: "JetBrains Mono",
    chartColors: ["#E94560", "#1A1A2E", "#0F3460", "#9CA3AF"],
    springConfig: { damping: 25, stiffness: 150, mass: 1 },
    transitionDuration: 0.5,
    captionHighlightColor: "#E94560",
    captionBackgroundColor: "rgba(250, 250, 250, 0.9)",
  },
  "premium-minimalist": {
    primaryColor: "#111827",
    accentColor: "#2563EB",
    backgroundColor: "#F9FAFB",
    surfaceColor: "#FFFFFF",
    textColor: "#111827",
    mutedTextColor: "#6B7280",
    headingFont: "Inter",
    bodyFont: "Inter",
    monoFont: "JetBrains Mono",
    chartColors: ["#111827", "#2563EB", "#0F766E", "#9CA3AF", "#374151"],
    springConfig: { damping: 26, stiffness: 140, mass: 1 },
    transitionDuration: 0.45,
    captionHighlightColor: "#2563EB",
    captionBackgroundColor: "rgba(249, 250, 251, 0.9)",
  },
  "anime-ghibli": {
    primaryColor: "#2D5016",
    accentColor: "#FFB347",
    backgroundColor: "#0A0A1A",
    surfaceColor: "#1A2332",
    textColor: "#F5F0E8",
    mutedTextColor: "#8B9A7E",
    headingFont: "Playfair Display",
    bodyFont: "Noto Sans",
    monoFont: "JetBrains Mono",
    chartColors: ["#FFB347", "#2D5016", "#FF6B9D", "#A8E6CF", "#6B4C8A", "#E8927C"],
    springConfig: { damping: 18, stiffness: 60, mass: 1 },
    transitionDuration: 1.0,
    captionHighlightColor: "#FFB347",
    captionBackgroundColor: "rgba(10, 10, 26, 0.8)",
  },
};

export const THEME_IDS = Object.keys(THEMES);

// The look used when a composition names no theme.
export const DEFAULT_THEME = THEMES["flat-motion-graphics"];

const THEME_FIELDS = new Set(Object.keys(DEFAULT_THEME));

// resolveTheme returns the preset named by props.theme (default
// flat-motion-graphics) with props.themeConfig fields laid over it.
export function resolveTheme(props: Record<string, unknown>): ThemeConfig {
  const name = props.theme;
  let base = DEFAULT_THEME;
  if (name !== undefined) {
    if (typeof name !== "string" || !THEMES[name]) {
      throw new Error(`theme must be one of ${THEME_IDS.join(", ")}`);
    }
    base = THEMES[name];
  }
  const override = props.themeConfig;
  if (override === undefined) {
    return base;
  }
  if (typeof override !== "object" || override === null || Array.isArray(override)) {
    throw new Error("themeConfig must be an object");
  }
  for (const key of Object.keys(override)) {
    if (!THEME_FIELDS.has(key)) {
      throw new Error(`themeConfig.${key} is not a theme field`);
    }
  }
  return { ...base, ...(override as Partial<ThemeConfig>) };
}
