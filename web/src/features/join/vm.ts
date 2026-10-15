// View-model helpers of the join feature. Owner: P0-W04 → P1-W02.
// i18n-dynamic: join:errors.
import { ApiError } from '../../api/errors';
import { authErrorKey } from '../auth/vm';

// Codes with join-specific wording (join:errors.<code>).
const codes = new Set(['handle_invalid', 'handle_taken', 'display_name_invalid', 'tos_required', 'age_required']);

// joinErrorKey returns the catalog key for a failure of the join flow, or
// undefined when nothing should be shown (a superseded passkey prompt).
export function joinErrorKey(err: unknown): string | undefined {
  if (err instanceof ApiError && codes.has(err.code)) return `join:errors.${err.code}`;
  return authErrorKey(err);
}

// languages are the browser's preferred languages as base tags (at most 10,
// distinct), used for the new account's reading languages.
export function browserLanguages(list: readonly string[]): string[] {
  const out: string[] = [];
  for (const l of list) {
    const base = l.toLowerCase().split('-')[0] ?? '';
    if (/^[a-z]{2,3}$/.test(base) && !out.includes(base) && out.length < 10) out.push(base);
  }
  return out.length > 0 ? out : ['en'];
}
