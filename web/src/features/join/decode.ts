// QR decoding of one camera frame (runs inside the scan worker; pure, so it is
// unit-tested directly). Owner: P0-W04 → P1-W02.
import jsQR from 'jsqr';
import { isDualFrame, isFrame } from '../invites/handshake';

// isInviteFrame matches a handshake frame: "pcc2.<hs>.<E_i>.<n>.<mac>" (the
// claim) or "pcc2d.<hs>.<n>.<mac>" (the second code of the dual-QR mode).
export function isInviteFrame(s: string): boolean {
  return isFrame(s) || isDualFrame(s);
}

// decodeFrame returns the handshake frame in an RGBA image, or null.
export function decodeFrame(data: Uint8ClampedArray, width: number, height: number): string | null {
  const code = jsQR(data, width, height, { inversionAttempts: 'attemptBoth' });
  if (!code || !isInviteFrame(code.data)) return null;
  return code.data;
}
