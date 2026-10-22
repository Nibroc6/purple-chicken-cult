// The handshake QR code with a feather in its centre (the invite nav icon).
// Error correction is Q (25 % of codewords recoverable): the feather's box
// takes about 5 % of the modules, and H would make the 138-character frame a
// version 12 code (65 modules) instead of version 10 (57), which measurably
// hurts decoding at the scanner's 640 px frames. The logo box is aligned to
// whole modules and keeps clear of the finder patterns. Owner: P1-W02.
import QRCode from 'qrcode';

// Font Awesome Free 7 "feather" (solid), CC BY 4.0, viewBox 0 0 512 512.
const FEATHER =
  'M352 0c41 0 80.3 16.3 109.2 45.2l5.5 5.5c29 29 45.3 68.3 45.3 109.2 0 24.1-5.7 47.6-16.2 68.8-1.9 3.7-5.3 6.5-9.3 7.7L374.5 270c-3.9 1.2-6.5 4.7-6.5 8.8 0 5.1 4.1 9.2 9.2 9.2l32.2 0c14.3 0 21.4 17.2 11.3 27.3l-22.4 22.4c-1.9 1.9-4.2 3.2-6.7 4l-81 24.3c-3.9 1.2-6.5 4.7-6.5 8.8 0 5.1 4.1 9.2 9.2 9.2 13.2 0 18.9 15.7 7.8 22.9-41.1 26.6-89.3 41.1-139 41.1l-86 0-48 48c-8.8 8.8-23.2 8.8-32 0s-8.8-23.2 0-32L256 224c8.8-8.8 8.8-23.2 0-32s-23.2-8.8-32 0L79.5 336.5c-5.7 5.7-15.5 1.7-15.5-6.4 0-67.9 27-133 75-181L242.8 45.2C271.7 16.3 311 0 352 0z';

// The feather's colour: the default theme's accent. The code sits on white in
// every theme, so it does not follow the theme (a dark theme's accent is pale).
const FEATHER_COLOR = '#6b2fd6';
const MARGIN = 2; // quiet zone in modules (the CSS padding adds the rest)
const LOGO = 0.22; // the logo box's side, as a share of the code's side

export interface FeatherCode {
  size: number; // modules per side
  // The logo box: modules [start, start + side) on both axes are left light.
  logo: { start: number; side: number };
  dark: (x: number, y: number) => boolean;
}

// featherCode encodes payload at level Q and reserves the centred logo box.
export function featherCode(payload: string): FeatherCode {
  const q = QRCode.create(payload, { errorCorrectionLevel: 'Q' });
  const size = q.modules.size;
  let side = Math.round(size * LOGO);
  if (side % 2 !== size % 2) side += 1; // centred on whole modules
  const start = (size - side) / 2;
  const inLogo = (v: number) => v >= start && v < start + side;
  return {
    size,
    logo: { start, side },
    dark: (x, y) => !(inLogo(x) && inLogo(y)) && q.modules.get(y, x) === 1,
  };
}

// drawFeatherQR draws payload's code on canvas, sharp at the device's pixel
// ratio for a display size of about cssPx.
export function drawFeatherQR(canvas: HTMLCanvasElement, payload: string, cssPx = 360): void {
  const code = featherCode(payload);
  const total = code.size + 2 * MARGIN;
  const scale = Math.max(1, Math.ceil((cssPx * (window.devicePixelRatio || 1)) / total));
  canvas.width = canvas.height = total * scale;
  const ctx = canvas.getContext('2d');
  if (!ctx) return;
  ctx.fillStyle = '#fff';
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.fillStyle = '#000';
  for (let y = 0; y < code.size; y++) {
    for (let x = 0; x < code.size; x++) {
      if (code.dark(x, y)) ctx.fillRect((x + MARGIN) * scale, (y + MARGIN) * scale, scale, scale);
    }
  }
  // The feather, inset by one module from the box so it never touches a module.
  const inner = (code.logo.side - 2) * scale;
  const at = (code.logo.start + 1 + MARGIN) * scale;
  ctx.save();
  ctx.translate(at, at);
  ctx.scale(inner / 512, inner / 512);
  ctx.fillStyle = FEATHER_COLOR;
  ctx.fill(new Path2D(FEATHER));
  ctx.restore();
}
