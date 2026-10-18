// Media view-models. Owner: P1-W04.
import { asRef, type MediaVM } from '../../vm/types';

export function toMediaVM(v: { ref: string; w: number; h: number; alt?: string }): MediaVM {
  return { ref: asRef(v.ref), w: v.w, h: v.h, ...(v.alt ? { alt: v.alt } : {}) };
}

// ACCEPT lists the formats the server takes (it sniffs the bytes anyway).
export const ACCEPT = 'image/jpeg,image/png,image/webp,image/gif';

// MAX_BYTES mirrors the server's 15 MiB limit (checked again there).
export const MAX_BYTES = 15 * 1024 * 1024;

// MAX_ALT mirrors the server's alt text limit (code points).
export const MAX_ALT = 1000;
