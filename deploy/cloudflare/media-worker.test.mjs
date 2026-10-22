// node --test deploy/cloudflare/media-worker.test.mjs
// Checks the Worker against the Go signer's vectors and the 403/404 paths.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { handle, verify } from './media-worker.js';

const vectors = JSON.parse(readFileSync(new URL('./testvectors.json', import.meta.url), 'utf8'));

test('accepts the Go signer vectors before expiry, rejects at and after it', async () => {
  for (const v of vectors) {
    const q = new URLSearchParams(v.query);
    assert.equal(await verify(v.key_hex, v.blob, q, v.e - 60), 'ok');
    assert.equal(await verify(v.key_hex, v.blob, q, v.e), 'expired');
    assert.equal(await verify(v.key_hex, v.blob, q, v.e - 3 * 3600), 'bad'); // too far ahead
  }
});

test('rejects tampering', async () => {
  const v = vectors[0];
  const now = v.e - 60;
  const base = new URLSearchParams(v.query);
  const mut = (f) => {
    const q = new URLSearchParams(base);
    f(q);
    return q;
  };
  for (const q of [
    mut((q) => q.set('v', 'AAAAAAAA')),
    mut((q) => q.set('e', String(v.e + 1))),
    mut((q) => q.delete('s')),
    mut((q) => q.append('e', String(v.e))),
    mut((q) => q.set('x', '1')),
  ]) {
    assert.equal(await verify(v.key_hex, v.blob, q, now), 'bad');
  }
  assert.equal(await verify(v.key_hex, vectors[1].blob, base, now), 'bad');
});

test('serves signed objects with media-origin headers and refuses the rest', async () => {
  const v = vectors[0];
  const env = {
    MEDIA_URL_KEY: v.key_hex,
    MEDIA: { get: async (k) => (k === v.blob ? { body: 'IMG', httpMetadata: { contentType: 'image/webp' } } : null) },
  };
  const ctx = { waitUntil() {} };
  const ok = await handle(new Request(`https://media.example/m/${v.blob}?${v.query}`), env, ctx, v.e - 600);
  assert.equal(ok.status, 200);
  assert.equal(await ok.text(), 'IMG');
  assert.equal(ok.headers.get('Content-Type'), 'image/webp');
  assert.equal(ok.headers.get('X-Content-Type-Options'), 'nosniff');
  assert.equal(ok.headers.get('Content-Security-Policy'), "sandbox; default-src 'none'");
  assert.equal(ok.headers.get('Content-Disposition'), 'inline');
  assert.equal(ok.headers.get('Cache-Control'), 'private, max-age=600');
  const unsigned = await handle(new Request(`https://media.example/m/${v.blob}`), env, ctx, v.e - 600);
  assert.equal(unsigned.status, 403);
  const expired = await handle(new Request(`https://media.example/m/${v.blob}?${v.query}`), env, ctx, v.e + 1);
  assert.equal(expired.status, 403);
  const post = await handle(new Request(`https://media.example/m/${v.blob}?${v.query}`, { method: 'POST' }), env, ctx, v.e - 600);
  assert.equal(post.status, 405);
  env.MEDIA.get = async () => null;
  const gone = await handle(new Request(`https://media.example/m/${v.blob}?${v.query}`), env, ctx, v.e - 600);
  assert.equal(gone.status, 404);
});
