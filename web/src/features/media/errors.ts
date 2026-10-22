// Upload error codes shown under a picture. Owner: P1-W04.
import { ApiError } from '../../api/errors';

const CODES = ['media_too_large', 'media_type', 'media_rejected', 'alt_too_long', 'rate_limited'] as const;
export type ErrCode = (typeof CODES)[number] | 'other';

// errorCode maps an upload failure to its catalog key.
// i18n-dynamic: media:errors.
export function errorCode(err: unknown): ErrCode {
  if (err instanceof ApiError) return CODES.find((c) => c === err.code) ?? 'other';
  return 'other';
}
