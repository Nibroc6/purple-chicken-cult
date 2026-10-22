// Signed media URLs, cached per (ref, variant) until shortly before they
// expire. A 403 from the media origin (an expired or refused URL; an <img>
// cannot read the status, so any load error counts) drops the entry and the
// image asks again. Owner: P1-W04.
import { mediaURL, type SignedURL, type Variant } from './api';

const MARGIN_MS = 60_000; // refresh a minute early

const cache = new Map<string, Promise<SignedURL>>();

const key = (ref: string, variant: Variant) => `${variant}\u0000${ref}`;

// signedURL returns a cached, still-valid URL or asks the server for one.
export function signedURL(ref: string, variant: Variant, now: () => number = Date.now): Promise<SignedURL> {
  const k = key(ref, variant);
  const hit = cache.get(k);
  if (hit) {
    return hit.then((u) => (u.expiresAt.getTime() - MARGIN_MS > now() ? u : refresh(ref, variant)));
  }
  return refresh(ref, variant);
}

// refresh forgets the cached URL and asks for a new one.
export function refresh(ref: string, variant: Variant): Promise<SignedURL> {
  const k = key(ref, variant);
  const p = mediaURL(ref, variant);
  cache.set(k, p);
  p.catch(() => {
    if (cache.get(k) === p) cache.delete(k);
  });
  return p;
}

// clearURLs empties the cache (tests, logout).
export function clearURLs(): void {
  cache.clear();
}
