// QR decoding of one camera frame (runs inside the scan worker; pure, so it is
// unit-tested directly). Owner: P0-W04 → P1-W02.
import jsQR from 'jsqr';

// FRAME_RE matches a P0 invite frame "pcc1.<hsid>.<window>.<mac>" (plan §4.7).
const FRAME_RE = /^pcc1\.[A-Za-z0-9_-]{22}\.[0-9]{1,15}\.[A-Za-z0-9_-]{14}$/;

export function isInviteFrame(s: string): boolean {
  return FRAME_RE.test(s);
}

// decodeFrame returns the invite frame in an RGBA image, or null.
export function decodeFrame(data: Uint8ClampedArray, width: number, height: number): string | null {
  const code = jsQR(data, width, height, { inversionAttempts: 'attemptBoth' });
  if (!code || !isInviteFrame(code.data)) return null;
  return code.data;
}
