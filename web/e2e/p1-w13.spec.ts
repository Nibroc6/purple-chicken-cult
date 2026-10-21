// P1-W13 end to end: the real handshake sound codec in Chromium, through the
// real binary. The encoder renders a sound into a WAV, Chromium's fake
// microphone plays it (--use-file-for-fake-audio-capture, looped), and the
// sound check page decodes it from getUserMedia with the real decoder, so the
// codes it shows are exactly the bits that were encoded. The same page plays
// a sound through a real AudioContext. Selectors avoid English text so the
// spec runs in en and in the pseudo-locale en-XA.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { chromium, expect, test, type Browser } from '@playwright/test';
import { soundWav } from '../src/features/handshake-audio/wav';

let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
});

const hex = (b: Uint8Array) => Array.from(b, (v) => v.toString(16).padStart(2, '0')).join('');

// withMicrophone starts a Chromium whose microphone plays wav in a loop.
async function withMicrophone(wav: Uint8Array, run: (b: Browser) => Promise<void>): Promise<void> {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pcc-mic-'));
  const file = path.join(dir, 'sound.wav');
  fs.writeFileSync(file, wav);
  const browser = await chromium.launch({
    args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', `--use-file-for-fake-audio-capture=${file}`],
  });
  try {
    await run(browser);
  } finally {
    await browser.close();
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

const bits = { ultra: Uint8Array.of(0xc0, 0xff, 0xee, 0x42), jingle: Uint8Array.of(0x5a, 0x17, 0xe3) };

test('the sound check hears both layers of a sound from the microphone', async ({ locale }) => {
  await withMicrophone(soundWav(bits, { quiet: false }), async (browser) => {
    const page = await browser.newPage({ locale });
    await page.goto(`${origin}/sound/check`);
    await page.getByTestId('sound-check-listen').click();
    await expect(page.getByTestId('sound-check-listening')).toBeVisible();
    await expect(page.getByTestId('sound-check-jingle')).toHaveAttribute('data-code', hex(bits.jingle), { timeout: 20_000 });
    await expect(page.getByTestId('sound-check-ultra')).toHaveAttribute('data-code', hex(bits.ultra));
    await expect(page.getByTestId('sound-check-volume')).toHaveCount(0);
    await expect(page.getByTestId('sound-check-error')).toHaveCount(0);
  });
});

test('quiet mode carries only the ultrasonic layer', async ({ locale }) => {
  await withMicrophone(soundWav(bits, { quiet: true }), async (browser) => {
    const page = await browser.newPage({ locale });
    await page.goto(`${origin}/sound/check`);
    await page.getByTestId('sound-check-listen').click();
    await expect(page.getByTestId('sound-check-ultra')).toHaveAttribute('data-code', hex(bits.ultra), { timeout: 20_000 });
    await expect(page.getByTestId('sound-check-no-jingle')).toBeVisible();
  });
});

test('the sound check plays a sound and shows its code', async ({ page }) => {
  await page.goto(`${origin}/sound/check`);
  await page.getByTestId('sound-check-play').click();
  const played = page.getByTestId('sound-check-played');
  await expect(played).toBeVisible({ timeout: 10_000 });
  expect(await played.getAttribute('data-code')).toMatch(/^[0-9a-f]{6} · [0-9a-f]{8}$/);
  await expect(page.getByTestId('sound-check-error')).toHaveCount(0);

  await page.getByTestId('sound-check-quiet').check();
  await page.getByTestId('sound-check-play').click();
  await expect(played).toHaveAttribute('data-code', /^[0-9a-f]{8}$/, { timeout: 10_000 });
});
