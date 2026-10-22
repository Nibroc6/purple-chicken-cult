// P1-W04 end to end, through the real binary (PCC_ENV=test: dev scanner, fs
// blob and evidence stores, the Go /m/{key} media origin):
//  1. mia uploads a JPEG carrying EXIF GPS on /media/check with alt text; the
//     thumb, display and original come back through signed URLs as WebP with
//     no EXIF; an unsigned, tampered, other-viewer or expired URL is 403; her
//     friend noa gets no URL for media that is in no post he can see; a 403
//     from the media origin makes the image ask for a fresh URL.
//  2. ola uploads the synthetic blocklisted pattern (testdata/scan/blocklist.txt;
//     never real imagery): media_rejected, and her account is held, so her next
//     clean upload is media_rejected too; a 50,000×50,000 PNG header is refused.
// Every image is generated in code. Selectors avoid English text so the spec
// runs in en and in en-XA.
import { deflateSync } from 'node:zlib';
import { expect, request as pwRequest, test, type Page } from '@playwright/test';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
});

// ---- generated images ----

const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

function crc32(b: Uint8Array): number {
  let c = 0xffffffff;
  for (const x of b) c = (crcTable[(c ^ x) & 0xff] ?? 0) ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function u32(n: number): Buffer {
  const b = Buffer.alloc(4);
  b.writeUInt32BE(n >>> 0);
  return b;
}

function chunk(type: string, data: Buffer): Buffer {
  const td = Buffer.concat([Buffer.from(type, 'latin1'), data]);
  return Buffer.concat([u32(data.length), td, u32(crc32(td))]);
}

// rgbPNG builds an RGB PNG of pixel(x, y), filter 0. stored = deflate with
// uncompressed blocks (byte-for-byte deterministic, so its digest is fixed).
function rgbPNG(w: number, h: number, pixel: (x: number, y: number) => [number, number, number], stored: boolean): Buffer {
  const raw = Buffer.alloc(h * (1 + 3 * w));
  let o = 0;
  for (let y = 0; y < h; y++) {
    raw[o++] = 0;
    for (let x = 0; x < w; x++) {
      for (const c of pixel(x, y)) raw[o++] = c;
    }
  }
  let z: Buffer;
  if (stored) {
    const parts: Buffer[] = [Buffer.from([0x78, 0x01])];
    for (let i = 0; i < raw.length || i === 0; i += 65535) {
      const blk = raw.subarray(i, i + 65535);
      const final = i + 65535 >= raw.length ? 1 : 0;
      const hdr = Buffer.alloc(5);
      hdr[0] = final;
      hdr.writeUInt16LE(blk.length, 1);
      hdr.writeUInt16LE(blk.length ^ 0xffff, 3);
      parts.push(hdr, blk);
      if (final) break;
    }
    let a = 1;
    let b = 0;
    for (const c of raw) {
      a = (a + c) % 65521;
      b = (b + a) % 65521;
    }
    parts.push(u32(((b << 16) | a) >>> 0));
    z = Buffer.concat(parts);
  } else {
    z = deflateSync(raw);
  }
  const ihdr = Buffer.concat([u32(w), u32(h), Buffer.from([8, 2, 0, 0, 0])]);
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr), chunk('IDAT', z), chunk('IEND', Buffer.alloc(0))]);
}

// blockedPNG is the synthetic pattern whose SHA-256 is in testdata/scan/blocklist.txt.
const blockedPNG = () => rgbPNG(32, 32, (x, y) => [x * 8, y * 8, 0xc0], true);
const cleanPNG = (seed: number) => rgbPNG(48, 32, (x, y) => [(x * 5 + seed) & 255, (y * 7) & 255, seed & 255], false);

// bombPNG is a valid PNG header claiming 50,000×50,000 pixels.
function bombPNG(): Buffer {
  const b = cleanPNG(1);
  b.writeUInt32BE(50_000, 16);
  b.writeUInt32BE(50_000, 20);
  b.writeUInt32BE(crc32(b.subarray(12, 29)), 29);
  return b;
}

const GPS_MARKER = Buffer.from([0x13, 0x57, 0x9b, 0xdf, 0x24, 0x68, 0xac, 0xe0]);

// gpsJPEG renders a JPEG in the browser (canvas) and splices in an APP1 Exif
// segment with a GPS IFD holding GPS_MARKER.
async function gpsJPEG(page: Page): Promise<Buffer> {
  const b64 = await page.evaluate(async () => {
    const c = document.createElement('canvas');
    c.width = 1400;
    c.height = 900;
    const g = c.getContext('2d');
    if (!g) throw new Error('no 2d context');
    const grad = g.createLinearGradient(0, 0, 1400, 900);
    grad.addColorStop(0, '#6b2fd6');
    grad.addColorStop(1, '#f0c000');
    g.fillStyle = grad;
    g.fillRect(0, 0, 1400, 900);
    const blob = await new Promise<Blob | null>((r) => {
      c.toBlob(r, 'image/jpeg', 0.9);
    });
    if (!blob) throw new Error('no jpeg');
    const bytes = new Uint8Array(await blob.arrayBuffer());
    let s = '';
    for (const x of bytes) s += String.fromCharCode(x);
    return btoa(s);
  });
  const jpeg = Buffer.from(b64, 'base64');
  const tiff = Buffer.alloc(8 + 18 + 18);
  tiff.write('II', 0, 'latin1');
  tiff.writeUInt16LE(42, 2);
  tiff.writeUInt32LE(8, 4);
  tiff.writeUInt16LE(1, 8); // IFD0: one entry, the GPS IFD pointer
  tiff.writeUInt16LE(0x8825, 10);
  tiff.writeUInt16LE(4, 12);
  tiff.writeUInt32LE(1, 14);
  tiff.writeUInt32LE(26, 18);
  tiff.writeUInt16LE(1, 26); // GPS IFD: GPSLatitude, RATIONAL
  tiff.writeUInt16LE(2, 28);
  tiff.writeUInt16LE(5, 30);
  tiff.writeUInt32LE(1, 32);
  tiff.writeUInt32LE(44, 36);
  const app1 = Buffer.concat([Buffer.from('Exif\0\0', 'latin1'), tiff, GPS_MARKER]);
  const seg = Buffer.alloc(4);
  seg[0] = 0xff;
  seg[1] = 0xe1;
  seg.writeUInt16BE(app1.length + 2, 2);
  return Buffer.concat([jpeg.subarray(0, 2), seg, app1, jpeg.subarray(2)]);
}

// ---- helpers ----

async function signIn(page: Page, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, origin).login(handle);
}

interface MediaJSON {
  ref: string;
  w: number;
  h: number;
  alt?: string;
}

async function upload(page: Page, file: { name: string; mimeType: string; buffer: Buffer }, alt: string) {
  await page.getByTestId('media-file').setInputFiles(file);
  await page.getByTestId('media-alt').last().fill(alt);
  const res = page.waitForResponse((r) => r.url().endsWith('/api/media') && r.request().method() === 'POST');
  await page.getByTestId('media-upload').click();
  return res;
}

async function signedURL(page: Page, ref: string, variant: string): Promise<{ status: number; url: string }> {
  const res = await page.request.get(`${origin}/api/media/${ref}/url?variant=${variant}`, { headers: sameOrigin });
  if (res.status() !== 200) return { status: res.status(), url: '' };
  return { status: 200, url: ((await res.json()) as { url: string }).url };
}

async function loaded(page: Page, variant: string) {
  await expect
    .poll(() =>
      page
        .locator(`img[data-variant=${variant}]`)
        .first()
        .evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth > 0),
    )
    .toBe(true);
}

test('uploads come back as metadata-free WebP through signed per-viewer URLs that expire', async ({ page }) => {
  test.setTimeout(90_000);
  const w = new World(await pwRequest.newContext({ baseURL: origin }), origin);
  await w.reset();
  await w.seed('p1-w04');
  await signIn(page, 'mia');
  await page.goto(`${origin}/media/check`);
  await expect(page.getByTestId('media-picker')).toBeVisible();

  const res = await upload(page, { name: 'trip.jpg', mimeType: 'image/jpeg', buffer: await gpsJPEG(page) }, 'a purple to yellow gradient');
  expect(res.status()).toBe(201);
  const mv = (await res.json()) as MediaJSON;
  expect(mv).toMatchObject({ w: 1400, h: 900, alt: 'a purple to yellow gradient' });
  const thumb = page.getByTestId('media-uploaded').locator('img[data-variant=thumb]');
  await expect(thumb).toHaveAttribute('alt', 'a purple to yellow gradient');
  await loaded(page, 'thumb');

  // A 403 from the media origin (an expired URL) makes the image ask again.
  let refused = 0;
  await page.route('**/m/**', async (route) => {
    if (refused === 0) {
      refused++;
      await route.fulfill({ status: 403, body: '' });
      return;
    }
    await route.continue();
  });
  const asked = page.waitForResponse((r) => r.url().includes(`/api/media/${mv.ref}/url?variant=display`) && r.status() === 200);
  await page.getByTestId('media-open').click();
  await asked;
  await loaded(page, 'display');
  expect(refused).toBe(1);
  await page.unroute('**/m/**');
  await page.getByTestId('media-original').click();
  await loaded(page, 'original');

  // Every variant is WebP without EXIF (no GPS), sized as specified.
  const sizes: Record<string, number> = { thumb: 320, display: 1080, original: 1400 };
  for (const variant of Object.keys(sizes)) {
    const { status, url } = await signedURL(page, mv.ref, variant);
    expect(status).toBe(200);
    const img = await page.request.get(url);
    expect(img.status()).toBe(200);
    expect(img.headers()['content-type']).toBe('image/webp');
    expect(img.headers()['x-content-type-options']).toBe('nosniff');
    expect(img.headers()['content-security-policy']).toBe("sandbox; default-src 'none'");
    expect(img.headers()['content-disposition']).toBe('inline');
    const body = await img.body();
    expect(body.subarray(0, 4).toString('latin1')).toBe('RIFF');
    expect(body.subarray(8, 12).toString('latin1')).toBe('WEBP');
    expect(body.includes(GPS_MARKER)).toBe(false);
    expect(body.includes(Buffer.from('Exif', 'latin1'))).toBe(false);
  }

  // Unsigned, tampered and other-viewer URLs are 403.
  const { url } = await signedURL(page, mv.ref, 'display');
  const u = new URL(url);
  expect(u.pathname).toMatch(/^\/m\/[a-z2-7]{26}$/);
  expect((await page.request.get(`${origin}${u.pathname}`)).status()).toBe(403);
  const tampered = new URL(url);
  tampered.searchParams.set('v', 'AAAAAAAA');
  expect((await page.request.get(tampered.toString())).status()).toBe(403);
  const later = new URL(url);
  later.searchParams.set('e', String(Number(later.searchParams.get('e')) + 3600));
  expect((await page.request.get(later.toString())).status()).toBe(403);

  // noa, mia's friend, gets no URL: the media is in no post he can see, and
  // mia's ref is hers.
  const noa = await pwRequest.newContext({ baseURL: origin });
  await new World(noa, origin).login('noa');
  const theirs = await noa.get(`${origin}/api/media/${mv.ref}/url?variant=thumb`, { headers: sameOrigin });
  expect(theirs.status()).toBe(404);
  expect(await theirs.text()).toBe('{"error":"not_found"}\n');
  await noa.dispose();

  // After the hour the signed URL is refused.
  expect((await page.request.get(url)).status()).toBe(200);
  await w.advance('2h');
  expect((await page.request.get(url)).status()).toBe(403);
});

test('a blocklisted image is held, the account is held, and bomb headers are refused', async ({ page }) => {
  const w = new World(await pwRequest.newContext({ baseURL: origin }), origin);
  await w.reset();
  await w.seed('p1-w04');
  const csrf = await signIn(page, 'ola');
  await page.goto(`${origin}/media/check`);

  const bad = await upload(page, { name: 'pattern.png', mimeType: 'image/png', buffer: blockedPNG() }, '');
  expect(bad.status()).toBe(422);
  expect(((await bad.json()) as { error: string }).error).toBe('media_rejected');
  await expect(page.getByTestId('media-error')).toHaveAttribute('data-code', 'media_rejected');
  await expect(page.getByTestId('media-uploaded')).toHaveCount(0);

  // The account is held: a clean picture is refused the same way.
  await page.getByTestId('media-remove').click(); // drop the refused one
  const clean = await upload(page, { name: 'clean.png', mimeType: 'image/png', buffer: cleanPNG(3) }, '');
  expect(clean.status()).toBe(422);
  expect(((await clean.json()) as { error: string }).error).toBe('media_rejected');

  // mia is not held; her bomb header is refused before anything is decoded.
  const miaCSRF = await signIn(page, 'mia');
  const post = (buffer: Buffer, token: string) =>
    page.request.post(`${origin}/api/media`, {
      headers: { ...sameOrigin, Origin: origin, 'X-PCC-CSRF': token },
      multipart: { file: { name: 'x.png', mimeType: 'image/png', buffer }, alt: '' },
    });
  const bomb = await post(bombPNG(), miaCSRF);
  expect(bomb.status()).toBe(422);
  expect(((await bomb.json()) as { error: string }).error).toBe('media_too_large');
  expect((await post(cleanPNG(4), miaCSRF)).status()).toBe(201);
  expect(csrf).not.toBe('');
});
