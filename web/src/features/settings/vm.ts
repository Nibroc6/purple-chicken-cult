// View-models and pure helpers of the settings feature. Owner: P0-W11 → P1-W10.
import { ApiError, errorKey } from '../../api/errors';
import type { A11yPrefs } from '../../lib/a11y';
import type { SettingsView } from './api';

export type FormatChoice = 'cards' | 'compact' | 'explorer';
export type Density = 'comfortable' | 'compact';
export type Contrast = 'normal' | 'high';
export type TextScale = 'small' | 'normal' | 'large' | 'xlarge';
export type PresetChoice = 'latest' | 'balanced' | 'top';

export const formats: readonly FormatChoice[] = ['cards', 'compact', 'explorer'];
export const densities: readonly Density[] = ['comfortable', 'compact'];
export const contrasts: readonly Contrast[] = ['normal', 'high'];
export const textScales: readonly TextScale[] = ['small', 'normal', 'large', 'xlarge'];
export const presets: readonly PresetChoice[] = ['latest', 'balanced', 'top'];

// UIPrefs mirrors the server's ui_prefs schema (gate/account.UIPrefs).
export interface UIPrefs {
  format: FormatChoice;
  density: Density;
  a11y: { contrast: Contrast; textScale: TextScale; reducedMotion: boolean; dyslexiaFont: boolean };
}

function pick<T extends string>(v: unknown, allowed: readonly T[], fallback: T): T {
  return typeof v === 'string' && (allowed as readonly string[]).includes(v) ? (v as T) : fallback;
}

function obj(v: unknown): Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

// toUIPrefs reads the stored ui_prefs JSON, filling defaults for anything absent.
export function toUIPrefs(raw: unknown): UIPrefs {
  const p = obj(raw);
  const a = obj(p.a11y);
  return {
    format: pick(p.format, formats, 'cards'),
    density: pick(p.density, densities, 'comfortable'),
    a11y: {
      contrast: pick(a.contrast, contrasts, 'normal'),
      textScale: pick(a.text_scale, textScales, 'normal'),
      reducedMotion: a.reduced_motion === true,
      dyslexiaFont: a.dyslexia_font === true,
    },
  };
}

// fromUIPrefs serializes prefs in the server's snake_case schema.
export function fromUIPrefs(p: UIPrefs): Record<string, unknown> {
  return {
    format: p.format,
    density: p.density,
    a11y: {
      contrast: p.a11y.contrast,
      text_scale: p.a11y.textScale,
      reduced_motion: p.a11y.reducedMotion,
      dyslexia_font: p.a11y.dyslexiaFont,
    },
  };
}

// toA11y maps prefs to the <html> switches of lib/a11y.
export function toA11y(p: UIPrefs): A11yPrefs {
  return {
    contrast: p.a11y.contrast,
    textScale: p.a11y.textScale,
    reducedMotion: p.a11y.reducedMotion,
    dyslexiaFont: p.a11y.dyslexiaFont,
  };
}

// canonicalTag returns the canonical BCP-47 form of tag, or null when invalid.
export function canonicalTag(tag: string): string | null {
  const s = tag.trim();
  if (s === '' || s.toLowerCase() === 'und') return null;
  try {
    const [c] = Intl.getCanonicalLocales(s);
    return c ?? null;
  } catch {
    return null;
  }
}

// translateOptions are the languages translate_to may name (translate_to ∈ languages).
export function translateOptions(languages: readonly string[]): string[] {
  const out: string[] = [];
  for (const l of languages) {
    const c = canonicalTag(l);
    if (c && !out.includes(c)) out.push(c);
  }
  return out;
}

export function presetChoice(s: SettingsView): PresetChoice {
  return s.rank_preset === 'latest' || s.rank_preset === 'top' ? s.rank_preset : 'balanced';
}

// The account Problem codes localized in the account namespace.
// i18n-dynamic: account:errors.
const accountCodes = new Set([
  'display_name_invalid',
  'locale_invalid',
  'lang_invalid',
  'translate_to_not_in_languages',
  'ui_prefs_invalid',
  'preset_unavailable',
  'confirm_mismatch',
]);

// accountErrorKey returns the catalog key for an account API error.
export function accountErrorKey(err: unknown): string {
  if (err instanceof ApiError && accountCodes.has(err.code)) return `account:errors.${err.code}`;
  return errorKey(err);
}

// errorParams are the ICU arguments of an error message.
export function errorParams(err: unknown): Record<string, number> {
  return err instanceof ApiError && err.retryAfterS !== undefined ? { seconds: err.retryAfterS } : {};
}

// handleMatches compares a typed handle with the account's the way the server
// does (case-insensitive, optional leading @).
export function handleMatches(typed: string, handle: string): boolean {
  return typed.trim().replace(/^@/, '').toLowerCase() === handle.toLowerCase();
}

// exportFileName names the downloaded export after the day it was made.
export function exportFileName(now: Date): string {
  return `pcc-export-${now.toISOString().slice(0, 10)}.zip`;
}
