// Token-only themes (ARCHITECTURE §15.2 A): the presets, the mapping of a
// theme's tokens onto the app's CSS custom properties, and a mirror of the
// server's contrast rules for live hints (gate/themes is the authority). A
// theme sets custom properties on an element through the CSSOM (never a style
// attribute or a stylesheet: style-src 'self' stays strict) and nothing else,
// so no theme can hide or restyle an integrity element beyond its tokens.
// The accessibility switches win over a theme: high contrast drops the theme's
// colors, a text size other than normal its font size, the dyslexia font its
// font. Owner: P1-W09.
import type { components } from '../../api/schema.gen';
import presetFile from './presets.json';

export type ThemeTokens = components['schemas']['ThemeTokens'];
export type ThemeColors = ThemeTokens['colors'];
export type ColorName = keyof ThemeColors;
export type FontId = NonNullable<ThemeTokens['font']>;
export type DensityId = NonNullable<ThemeTokens['density']>;

export interface Preset {
  id: string;
  tokens: ThemeTokens;
}

export const presets: readonly Preset[] = presetFile.presets as Preset[];

export const colorNames: readonly ColorName[] = ['bg', 'surface', 'text', 'muted', 'accent', 'border', 'integrity_bg', 'integrity_text'];
export const fonts: readonly FontId[] = ['system', 'serif', 'humanist', 'rounded', 'mono', 'hyperlegible'];
export const densities: readonly DensityId[] = ['compact', 'comfortable', 'spacious'];
export const RADIUS = { min: 0, max: 24 } as const;
export const FONT_SIZE = { min: 14, max: 24 } as const;

// Local font stacks only: no font is downloaded, and no user text reaches CSS.
const fontStacks: Record<FontId, string> = {
  system: "system-ui, -apple-system, 'Segoe UI', Roboto, 'Noto Sans', sans-serif",
  serif: "'Iowan Old Style', 'Palatino Linotype', Georgia, 'Noto Serif', serif",
  humanist: "Seravek, 'Gill Sans Nova', Ubuntu, Calibri, 'DejaVu Sans', sans-serif",
  rounded: "ui-rounded, 'Hiragino Maru Gothic ProN', Quicksand, Comfortaa, 'Arial Rounded MT', sans-serif",
  mono: "ui-monospace, 'Cascadia Code', 'Source Code Pro', Menlo, Consolas, 'DejaVu Sans Mono', monospace",
  hyperlegible: "'Atkinson Hyperlegible', Verdana, 'Segoe UI', sans-serif",
};

// The spacing unit of each density (the scale is 1, 2, 3, 4 and 6 units).
const units: Record<DensityId, number> = { compact: 3, comfortable: 4, spacious: 5 };
const steps = [1, 2, 3, 4, 6] as const;

const colorProps: Record<ColorName, string> = {
  bg: '--color-bg',
  surface: '--color-surface',
  text: '--color-text',
  muted: '--color-muted',
  accent: '--color-accent',
  border: '--color-border',
  integrity_bg: '--color-integrity-bg',
  integrity_text: '--color-integrity-text',
};

// themeProps lists every property a theme may set (cleared before each apply).
export const themeProps: readonly string[] = [
  ...Object.values(colorProps),
  '--radius',
  ...steps.map((s) => `--space-${String(s)}`),
  '--font-body',
  '--font-size',
];

const hex = /^#[0-9a-fA-F]{6}$/;

// overrides reads the accessibility switches that win over a theme.
export interface A11yOverrides {
  highContrast: boolean;
  textScaled: boolean;
  dyslexiaFont: boolean;
}

export function overridesOf(root: HTMLElement): A11yOverrides {
  return {
    highContrast: root.dataset.contrast === 'high',
    textScaled: (root.dataset.textScale ?? 'normal') !== 'normal',
    dyslexiaFont: root.dataset.dyslexiaFont === 'true',
  };
}

// themeVars maps tokens onto custom properties under the accessibility switches.
export function themeVars(tokens: ThemeTokens, o: A11yOverrides): Map<string, string> {
  const vars = new Map<string, string>();
  if (!o.highContrast) {
    for (const c of colorNames) {
      const v = tokens.colors[c];
      if (hex.test(v)) vars.set(colorProps[c], v.toLowerCase());
    }
  }
  if (tokens.radius !== undefined) vars.set('--radius', `${String(clamp(tokens.radius, RADIUS.min, RADIUS.max))}px`);
  if (tokens.density !== undefined && tokens.density in units) {
    const u = units[tokens.density];
    for (const s of steps) vars.set(`--space-${String(s)}`, `${String(u * s)}px`);
  }
  if (tokens.font !== undefined && !o.dyslexiaFont && tokens.font in fontStacks) vars.set('--font-body', fontStacks[tokens.font]);
  if (tokens.font_size !== undefined && !o.textScaled) {
    vars.set('--font-size', `${String(clamp(tokens.font_size, FONT_SIZE.min, FONT_SIZE.max))}px`);
  }
  return vars;
}

// applyTheme sets el's theme properties (undefined clears them).
export function applyTheme(el: HTMLElement, tokens: ThemeTokens | undefined, o: A11yOverrides): void {
  for (const p of themeProps) el.style.removeProperty(p);
  if (!tokens) return;
  for (const [p, v] of themeVars(tokens, o)) el.style.setProperty(p, v);
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, Math.round(n)));
}

// contrast is the WCAG 2 contrast ratio of two "#rrggbb" colors.
export function contrast(a: string, b: string): number {
  const lum = (h: string) => {
    const ch = [1, 3, 5].map((i) => {
      const v = parseInt(h.slice(i, i + 2), 16) / 255;
      return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * (ch[0] ?? 0) + 0.7152 * (ch[1] ?? 0) + 0.0722 * (ch[2] ?? 0);
  };
  const x = lum(a);
  const y = lum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

export type CheckId = 'integrity' | 'integrityOutline' | 'text' | 'muted' | 'links';

export interface Check {
  id: CheckId;
  ratio: number; // the worst pair of the check
  min: number;
  ok: boolean;
}

// The server's pairs (gate/themes), grouped for the page.
const checkPairs: Record<CheckId, { pairs: [ColorName, ColorName][]; min: number }> = {
  integrity: { pairs: [['integrity_text', 'integrity_bg']], min: 4.5 },
  integrityOutline: {
    pairs: [
      ['integrity_text', 'surface'],
      ['integrity_text', 'bg'],
    ],
    min: 3,
  },
  text: {
    pairs: [
      ['text', 'surface'],
      ['text', 'bg'],
    ],
    min: 4.5,
  },
  muted: {
    pairs: [
      ['muted', 'surface'],
      ['muted', 'bg'],
    ],
    min: 4.5,
  },
  links: {
    pairs: [
      ['accent', 'surface'],
      ['accent', 'bg'],
    ],
    min: 4.5,
  },
};

export function checks(c: ThemeColors): Check[] {
  return (Object.keys(checkPairs) as CheckId[]).map((id) => {
    const { pairs, min } = checkPairs[id];
    const ratio = Math.min(...pairs.map(([fg, bg]) => (hex.test(c[fg]) && hex.test(c[bg]) ? contrast(c[fg], c[bg]) : 1)));
    return { id, ratio, min, ok: ratio >= min };
  });
}

// presetOf is the preset a theme equals, if any.
export function presetOf(tokens: ThemeTokens | undefined): string | undefined {
  if (!tokens) return undefined;
  const key = JSON.stringify(normalize(tokens));
  return presets.find((p) => JSON.stringify(normalize(p.tokens)) === key)?.id;
}

function normalize(t: ThemeTokens): ThemeTokens {
  const colors = Object.fromEntries(colorNames.map((c) => [c, t.colors[c].toLowerCase()])) as ThemeColors;
  return {
    colors,
    ...(t.radius !== undefined ? { radius: t.radius } : {}),
    ...(t.density !== undefined ? { density: t.density } : {}),
    ...(t.font !== undefined ? { font: t.font } : {}),
    ...(t.font_size !== undefined ? { font_size: t.font_size } : {}),
  };
}
