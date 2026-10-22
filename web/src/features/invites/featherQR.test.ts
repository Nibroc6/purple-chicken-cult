// The feathered handshake QR code still decodes (P1-W02).
import jsQR from 'jsqr';
import QRCode from 'qrcode';
import { describe, expect, it } from 'vitest';
import { featherCode } from './featherQR';
import { renderDualFrame, renderFrame } from './handshake';

// image rasterises a module predicate as RGBA (4 px per module, 4-module quiet zone).
function image(size: number, dark: (x: number, y: number) => boolean) {
  const scale = 4;
  const side = (size + 8) * scale;
  const data = new Uint8ClampedArray(side * side * 4).fill(255);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      if (!dark(x, y)) continue;
      for (let dy = 0; dy < scale; dy++) {
        for (let dx = 0; dx < scale; dx++) {
          const i = (((y + 4) * scale + dy) * side + (x + 4) * scale + dx) * 4;
          data[i] = data[i + 1] = data[i + 2] = 0;
        }
      }
    }
  }
  return { data, side };
}

const decode = (size: number, dark: (x: number, y: number) => boolean) => {
  const { data, side } = image(size, dark);
  return jsQR(data, side, side)?.data;
};

async function payloads(): Promise<string[]> {
  const key = new Uint8Array(32).fill(3);
  const hs = new Uint8Array(16).fill(7);
  const ei = new Uint8Array(65).map((_, i) => (i * 37 + 11) & 255);
  return [
    await renderFrame(key, { hs, ei, n: 0 }),
    await renderFrame(key, { hs, ei, n: 9_999_999_999 }),
    await renderDualFrame(key, hs, 123_456),
  ];
}

describe('feather QR', () => {
  it('decodes with the logo box cleared', async () => {
    for (const p of await payloads()) {
      const c = featherCode(p);
      expect(decode(c.size, c.dark)).toBe(p);
    }
  });

  it('decodes even with every module under the logo wrong', async () => {
    // The worst any drawing in the box can do: each module there flipped.
    for (const p of await payloads()) {
      const c = featherCode(p);
      const q = QRCode.create(p, { errorCorrectionLevel: 'Q' });
      const { start, side } = c.logo;
      const inLogo = (v: number) => v >= start && v < start + side;
      const flipped = (x: number, y: number) => (inLogo(x) && inLogo(y) ? !q.modules.get(y, x) : c.dark(x, y));
      expect(decode(c.size, flipped)).toBe(p);
    }
  });

  it('keeps the logo centred and clear of the finder patterns', async () => {
    const [frame = ''] = await payloads();
    const c = featherCode(frame);
    expect(c.size).toBe(57); // version 10 at level Q
    expect(c.logo.start * 2 + c.logo.side).toBe(c.size);
    expect(c.logo.start).toBeGreaterThan(8);
    expect(c.logo.side * c.logo.side).toBeLessThan(0.06 * c.size * c.size);
  });
});
