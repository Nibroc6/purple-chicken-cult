// View-models and error keys of the auth feature. Owner: P0-W01 → P1-W07.
// i18n-dynamic: auth:errors.
// i18n-dynamic: auth:passkey.
import { ApiError, errorKey, errorMsg, type ErrorMsg, errorParams } from '../../api/errors';
import type { PasskeyJSON } from './api';
import { CancelledError } from './passkeyFlow';
import { PasskeyError } from './webauthn';

export interface PasskeyVM {
  id: string;
  name: string;
  createdAt: Date;
  lastUsedAt: Date | undefined;
  current: boolean;
}

export function toPasskeyVM(p: PasskeyJSON): PasskeyVM {
  return {
    id: p.id,
    name: p.name,
    createdAt: new Date(p.created_at),
    lastUsedAt: p.last_used_at ? new Date(p.last_used_at) : undefined,
    current: p.current,
  };
}

// Codes with auth-specific wording (auth:errors.<code>); the rest use errors:<code>.
const authCodes = new Set(['last_passkey', 'passkey_rejected', 'passkey_name_invalid', 'tos_required', 'age_required']);

// authErrorKey returns the catalog key for a failure of an auth flow, or
// undefined when nothing should be shown (a superseded ceremony).
export function authErrorKey(err: unknown): string | undefined {
  if (err instanceof CancelledError) return undefined;
  if (err instanceof PasskeyError) return err.code === 'aborted' ? undefined : `auth:passkey.${err.code}`;
  if (err instanceof ApiError && authCodes.has(err.code)) return `auth:errors.${err.code}`;
  return errorKey(err);
}

// errorParams are the shared ICU arguments of an error message (api/errors).
export { errorParams };

export type { ErrorMsg };

// authError is authErrorKey with the arguments, or undefined when nothing
// should be shown.
export function authError(err: unknown): ErrorMsg | undefined {
  const key = authErrorKey(err);
  return key ? errorMsg(err, key) : undefined;
}

// readFragmentToken takes the enrollment token from the URL fragment and
// removes it from the address bar and history (it is a one-time secret that
// must never reach a server log, a referrer or a screenshot of the URL bar).
export function readFragmentToken(loc: Location, hist: History): string | undefined {
  const raw = loc.hash.startsWith('#') ? loc.hash.slice(1) : loc.hash;
  if (raw) hist.replaceState(hist.state, '', loc.pathname + loc.search);
  return /^[A-Za-z0-9_-]{20,64}$/.test(raw) ? raw : undefined;
}
