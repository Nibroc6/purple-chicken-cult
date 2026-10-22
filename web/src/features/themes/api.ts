// Theme API calls (plan §5.3 themeapi). Owner: P1-W09.
import { api, unwrap } from '../../api/client';
import type { components } from '../../api/schema.gen';
import type { ThemeTokens } from './theme';

export type ThemeView = components['schemas']['ThemeView'];

export const themeKey = ['me', 'theme'] as const;

export async function fetchTheme(): Promise<ThemeView | null> {
  return unwrap(await api.GET('/me/theme')).theme ?? null;
}

export type ThemeImage = components['schemas']['ThemeImage'];

// saveTheme stores tokens and, in advanced mode, theme CSS and its images
// (fresh upload refs or the refs of the theme's current images).
export async function saveTheme(tokens: ThemeTokens, css = '', images: readonly { name: string; ref: string }[] = []): Promise<ThemeView | null> {
  const body = css === '' ? { tokens } : { tokens, css, images: images.map((i) => ({ name: i.name, ref: i.ref })) };
  return unwrap(await api.PUT('/me/theme', { body })).theme ?? null;
}

export async function clearTheme(): Promise<void> {
  unwrap(await api.DELETE('/me/theme'));
}
