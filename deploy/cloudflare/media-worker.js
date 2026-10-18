// Media origin Worker (plan §4.7, security #27). Serves media blobs from the
// PRIVATE R2 bucket bound as MEDIA, only for URLs signed by the app:
//
//   https://<media host>/m/<key>?e=<unix>&v=<base64url(tag)>&s=<base64url(sig)>
//   sig = HMAC-SHA256(MEDIA_URL_KEY, key (26 B ascii) ‖ e (8 B big-endian) ‖ v (6 B))
//
// The HMAC and the expiry are checked BEFORE the cache lookup, so a cached
// object is never served to an unsigned or expired URL. The cache key is the
// path only (it excludes e, v and s), so every viewer's URL shares one cached
// copy, and deleting media purges that one key (gate/media → zone purge API).
// Responses carry the media-origin headers: nosniff, a sandboxing CSP and
// Content-Disposition: inline. Anything else is 403 (bad or expired signature),
// 404 (no such object) or 405 (method).
//
// Bindings (wrangler.toml): MEDIA (R2 bucket), MEDIA_URL_KEY (secret, 64 hex =
// HKDF-SHA256(PCC_MASTER_KEY, info "pcc/media-url/v1"), see
// deploy/runbooks/media.md), CORP (optional, default "same-site").
// Must match internal/platform/blob/sign.go; deploy/cloudflare/testvectors.json
// is checked by both (node --test deploy/cloudflare/media-worker.test.mjs).

const KEY_RE = /^[a-z2-7]{26}$/;
const MAX_LIFETIME_S = 2 * 60 * 60; // the app signs at most 1 h ahead
const EDGE_TTL_S = 60 * 60; // never cache longer than a URL lives

function b64url(s) {
  if (!/^[A-Za-z0-9_-]*$/.test(s)) return null;
  const pad = s.replace(/-/g, '+').replace(/_/g, '/');
  try {
    const bin = atob(pad + '='.repeat((4 - (pad.length % 4)) % 4));
    return Uint8Array.from(bin, (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
}

function hexBytes(h) {
  if (!/^([0-9a-f]{2})+$/.test(h)) throw new Error('MEDIA_URL_KEY must be hex');
  return Uint8Array.from(h.match(/../g), (x) => parseInt(x, 16));
}

const keyCache = new Map();
async function hmacKey(hex) {
  let k = keyCache.get(hex);
  if (!k) {
    k = await crypto.subtle.importKey('raw', hexBytes(hex), { name: 'HMAC', hash: 'SHA-256' }, false, ['verify']);
    keyCache.set(hex, k);
  }
  return k;
}

// verify returns 'ok', 'expired' or 'bad' for blob key and the query params.
export async function verify(keyHex, key, params, nowS) {
  if (!KEY_RE.test(key)) return 'bad';
  const names = [...params.keys()];
  if (names.length !== 3 || params.getAll('e').length !== 1 || params.getAll('v').length !== 1 || params.getAll('s').length !== 1) {
    return 'bad';
  }
  const es = params.get('e');
  const vs = params.get('v');
  const ss = params.get('s');
  if (!/^[1-9][0-9]{0,11}$/.test(es) || vs.length !== 8 || ss.length !== 43) return 'bad';
  const v = b64url(vs);
  const s = b64url(ss);
  if (!v || !s || v.length !== 6 || s.length !== 32) return 'bad';
  const e = Number(es);
  const msg = new Uint8Array(26 + 8 + 6);
  msg.set(new TextEncoder().encode(key), 0);
  new DataView(msg.buffer).setBigUint64(26, BigInt(e), false);
  msg.set(v, 34);
  const ok = await crypto.subtle.verify('HMAC', await hmacKey(keyHex), s, msg); // constant time
  if (!ok) return 'bad';
  if (nowS >= e) return 'expired';
  if (e - nowS > MAX_LIFETIME_S) return 'bad';
  return 'ok';
}

function originHeaders(env, extra) {
  const h = new Headers(extra);
  h.set('X-Content-Type-Options', 'nosniff');
  h.set('Content-Security-Policy', "sandbox; default-src 'none'");
  h.set('Content-Disposition', 'inline');
  h.set('Cross-Origin-Resource-Policy', env.CORP || 'same-site');
  h.set('Referrer-Policy', 'no-referrer');
  h.set('X-Robots-Tag', 'noindex, nofollow, noai, noimageai');
  h.set('tdm-reservation', '1');
  return h;
}

function deny(env, status) {
  return new Response(null, { status, headers: originHeaders(env, { 'Cache-Control': 'no-store' }) });
}

export async function handle(request, env, ctx, nowS = Math.floor(Date.now() / 1000)) {
  if (request.method !== 'GET' && request.method !== 'HEAD') return deny(env, 405);
  const url = new URL(request.url);
  const m = /^\/m\/([^/]+)$/.exec(url.pathname);
  if (!m) return deny(env, 404);
  const key = m[1];
  const verdict = await verify(env.MEDIA_URL_KEY, key, url.searchParams, nowS);
  if (verdict !== 'ok') return deny(env, 403);
  const e = Number(url.searchParams.get('e'));
  const clientTTL = Math.max(0, Math.min(EDGE_TTL_S, e - nowS));

  const cache = globalThis.caches ? globalThis.caches.default : null;
  const cacheKey = new Request(`${url.origin}/m/${key}`, { method: 'GET' }); // excludes e, v, s
  let res = cache ? await cache.match(cacheKey) : undefined;
  if (!res) {
    const obj = await env.MEDIA.get(key);
    if (!obj) return deny(env, 404);
    const h = originHeaders(env, { 'Content-Type': (obj.httpMetadata && obj.httpMetadata.contentType) || 'application/octet-stream' });
    h.set('Cache-Control', `public, max-age=${EDGE_TTL_S}`);
    res = new Response(obj.body, { status: 200, headers: h });
    if (cache) ctx.waitUntil(cache.put(cacheKey, res.clone()));
  }
  const out = new Response(request.method === 'HEAD' ? null : res.body, { status: 200, headers: originHeaders(env, res.headers) });
  out.headers.set('Cache-Control', `private, max-age=${clientTTL}`);
  return out;
}

export default {
  fetch(request, env, ctx) {
    return handle(request, env, ctx);
  },
};
