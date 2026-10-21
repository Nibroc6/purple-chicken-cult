// Theme API calls (plan §5.3 themeapi). Owner: P1-W09.
import { api, unwrap } from '../../api/client';
import type { components } from '../../api/schema.gen';
import type { ThemeTokens } from './theme';

export type ThemeView = components['schemas']['ThemeView'];

export const themeKey = ['me', 'theme'] as const;

export async function fetchTheme(): Promise<ThemeView | null> {
  return unwrap(await api.GET('/me/theme')).theme ?? null;
}

export async function saveTheme(tokens: ThemeTokens): Promise<ThemeView | null> {
  return unwrap(await api.PUT('/me/theme', { body: { tokens } })).theme ?? null;
}

export async function clearTheme(): Promise<void> {
  unwrap(await api.DELETE('/me/theme'));
}
