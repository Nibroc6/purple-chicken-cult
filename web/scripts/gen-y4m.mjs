// Renders a QR code as a Y4M video for Chromium's fake camera
// (--use-file-for-fake-video-capture), so e2e specs can scan an invite frame
// without a real camera (plan §7, P0-W04). Grayscale 4:2:0, a few identical
// frames (Chromium loops the file).
// Usage: node scripts/gen-y4m.mjs <payload> <out.y4m>
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';
import QRCode from 'qrcode';

const W = 640;
const H = 480;

// y4m returns the Y4M bytes of text rendered as a centered QR code.
export function y4m(text, frames = 10) {
  const q = QRCode.create(text, { errorCorrectionLevel: 'M' });
  const n = q.modules.size;
  const scale = Math.floor((Math.min(W, H) - 40) / (n + 8));
  const side = (n + 8) * scale;
  const x0 = Math.floor((W - side) / 2);
  const y0 = Math.floor((H - side) / 2);
  const luma = Buffer.alloc(W * H, 235);
  for (let y = 0; y < n; y++) {
    for (let x = 0; x < n; x++) {
      if (!q.modules.get(y, x)) continue;
      for (let dy = 0; dy < scale; dy++) {
        const row = (y0 + (y + 4) * scale + dy) * W + x0 + (x + 4) * scale;
        luma.fill(16, row, row + scale);
      }
    }
  }
  const chroma = Buffer.alloc((W / 2) * (H / 2) * 2, 128);
  const parts = [Buffer.from(`YUV4MPEG2 W${W} H${H} F10:1 Ip A1:1 C420jpeg\n`)];
  for (let i = 0; i < frames; i++) parts.push(Buffer.from('FRAME\n'), luma, chroma);
  return Buffer.concat(parts);
}

// writeY4M writes the video of text to file.
export function writeY4M(text, file) {
  fs.writeFileSync(file, y4m(text));
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  const [text, out] = process.argv.slice(2);
  if (!text || !out) {
    console.error('usage: node scripts/gen-y4m.mjs <payload> <out.y4m>');
    process.exit(2);
  }
  writeY4M(text, out);
}
