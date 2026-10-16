// The in-person handshake protocol v2 on the device (ARCHITECTURE §7, plan
// P1-W02): ephemeral ECDH P-256 keys, the 250 ms QR frames rendered from the
// server's frame key, kmac = HKDF(ECDH secret), the sound bits, the dual-QR
// frame, and the claiming device's separate non-extractable ECDSA key that
// signs the claim and every later join call. Byte for byte what
// internal/gate/invites checks (handshake.test.ts pins the shared vectors).
// No audio is handled here; the sound itself is features/handshake-audio.
// Owner: P1-W02.

export const FRAME_MS = 250;

const enc = new TextEncoder();

// bytes copies u into a fresh ArrayBuffer-backed array (WebCrypto's BufferSource).
function bytes(u: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(new ArrayBuffer(u.length));
  out.set(u);
  return out;
}

export function concat(...parts: Uint8Array[]): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(new ArrayBuffer(parts.reduce((n, p) => n + p.length, 0)));
  let i = 0;
  for (const p of parts) {
    out.set(p, i);
    i += p.length;
  }
  return out;
}

export function b64url(u: Uint8Array): string {
  let s = '';
  for (const b of u) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function fromB64url(s: string): Uint8Array<ArrayBuffer> {
  if (!/^[A-Za-z0-9_-]*$/.test(s)) throw new Error('handshake: not base64url');
  const bin = atob(s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (s.length % 4)) % 4));
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function be64(n: number): Uint8Array {
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigUint64(0, BigInt(n));
  return out;
}

// uuidBytes parses a UUID in its canonical form.
export function uuidBytes(id: string): Uint8Array<ArrayBuffer> {
  const hex = id.replace(/-/g, '');
  if (!/^[0-9a-f]{32}$/i.test(hex)) throw new Error('handshake: not a uuid');
  const out = new Uint8Array(new ArrayBuffer(16));
  for (let i = 0; i < 16; i++) out[i] = parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  return out;
}

async function hmac(key: Uint8Array, ...parts: Uint8Array[]): Promise<Uint8Array> {
  const k = await crypto.subtle.importKey('raw', bytes(key), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  return new Uint8Array(await crypto.subtle.sign('HMAC', k, concat(...parts)));
}

export async function sha256(data: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', bytes(data)));
}

// ---- frames ----

export interface Frame {
  hs: Uint8Array; // 16-byte handshake id
  ei: Uint8Array; // the initiator's E_i
  n: number; // frames since the handshake opened (nonce_t)
}

const FRAME_RE = /^pcc2\.([A-Za-z0-9_-]{22})\.([A-Za-z0-9_-]{87})\.(0|[1-9][0-9]{0,14})\.([A-Za-z0-9_-]{14})$/;
const DUAL_RE = /^pcc2d\.([A-Za-z0-9_-]{22})\.(0|[1-9][0-9]{0,14})\.([A-Za-z0-9_-]{14})$/;

export function isFrame(s: string): boolean {
  return FRAME_RE.test(s);
}

export function isDualFrame(s: string): boolean {
  return DUAL_RE.test(s);
}

// parseFrame splits "pcc2.<hs>.<E_i>.<n>.<mac>" (the MAC is the server's to check).
export function parseFrame(s: string): Frame | undefined {
  const m = FRAME_RE.exec(s);
  if (!m) return undefined;
  return { hs: fromB64url(m[1] ?? ''), ei: fromB64url(m[2] ?? ''), n: Number(m[3]) };
}

// renderFrame is the payload of frame n: mac = HMAC(frame_key, "pcc-frame-v2" ‖ hs ‖ E_i ‖ n)[:10].
export async function renderFrame(frameKey: Uint8Array, f: Frame): Promise<string> {
  const mac = (await hmac(frameKey, enc.encode('pcc-frame-v2'), f.hs, f.ei, be64(f.n))).slice(0, 10);
  return `pcc2.${b64url(f.hs)}.${b64url(f.ei)}.${String(f.n)}.${b64url(mac)}`;
}

// renderDualFrame is the dual-QR payload of frame n under kmac.
export async function renderDualFrame(kmac: Uint8Array, hs: Uint8Array, n: number): Promise<string> {
  const mac = (await hmac(kmac, enc.encode('pcc-dual-v2'), hs, be64(n))).slice(0, 10);
  return `pcc2d.${b64url(hs)}.${String(n)}.${b64url(mac)}`;
}

// frameAt is the frame counter at time now (ms) given a synced frame n0 at t0.
export function frameAt(n0: number, t0: number, now: number): number {
  return n0 + Math.max(0, Math.floor((now - t0) / FRAME_MS));
}

// ---- keys ----

export interface Ephemeral {
  priv: CryptoKey; // non-extractable
  pub: Uint8Array; // uncompressed point
}

export async function ephemeral(): Promise<Ephemeral> {
  const k = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
  return { priv: k.privateKey, pub: new Uint8Array(await crypto.subtle.exportKey('raw', k.publicKey)) };
}

// kmacFromShared is HKDF-SHA256(S, salt = hs, "pcc-hs-v2 kmac", 32 bytes).
export async function kmacFromShared(shared: Uint8Array, hs: Uint8Array): Promise<Uint8Array> {
  const ikm = await crypto.subtle.importKey('raw', bytes(shared), 'HKDF', false, ['deriveBits']);
  const out = await crypto.subtle.deriveBits(
    { name: 'HKDF', hash: 'SHA-256', salt: bytes(hs), info: enc.encode('pcc-hs-v2 kmac') },
    ikm,
    256,
  );
  return new Uint8Array(out);
}

// kmac derives the shared MAC key from our ephemeral key and the peer's point.
export async function kmac(own: Ephemeral, peer: Uint8Array, hs: Uint8Array): Promise<Uint8Array> {
  const pub = await crypto.subtle.importKey('raw', bytes(peer), { name: 'ECDH', namedCurve: 'P-256' }, false, []);
  const shared = new Uint8Array(await crypto.subtle.deriveBits({ name: 'ECDH', public: pub }, own.priv, 256));
  return kmacFromShared(shared, hs);
}

// ---- the sound ----

export function transcript(hs: Uint8Array, ei: Uint8Array, ev: Uint8Array, n: number, attempt: number): Uint8Array {
  return concat(enc.encode('pcc-hs-v2'), hs, ei, ev, be64(n), Uint8Array.of(attempt));
}

// soundBits are the ultrasonic (4 B) and jingle (3 B) bits of a transcript.
export async function soundBits(k: Uint8Array, tr: Uint8Array): Promise<{ ultra: Uint8Array; jingle: Uint8Array }> {
  const ultra = (await hmac(k, tr)).slice(0, 4);
  const jingle = (await hmac(k, tr, enc.encode('jingle'))).slice(0, 3);
  return { ultra, jingle };
}

export async function commit(k: Uint8Array): Promise<string> {
  return b64url(await sha256(k));
}

// ---- the claiming device ----

export interface ClaimBody {
  payload: string;
  e_v: string;
  sig_pub: string;
  proof: string;
}

// ClaimDevice holds the claiming device's keys for one handshake.
export class ClaimDevice {
  private constructor(
    readonly ecdh: Ephemeral,
    private readonly sig: CryptoKeyPair,
    readonly sigPub: Uint8Array,
  ) {}

  frame?: Frame;
  private challenge?: Uint8Array;

  static async create(): Promise<ClaimDevice> {
    const sig = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign', 'verify']);
    const pub = new Uint8Array(await crypto.subtle.exportKey('raw', sig.publicKey));
    return new ClaimDevice(await ephemeral(), sig, pub);
  }

  private async sign(msg: Uint8Array): Promise<string> {
    // WebCrypto ECDSA signatures are P1363 (r ‖ s), what the server expects.
    return b64url(new Uint8Array(await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, this.sig.privateKey, bytes(msg))));
  }

  // claim scans payload: the claim body with E_v, the signing key and the proof.
  async claim(payload: string): Promise<ClaimBody> {
    const f = parseFrame(payload);
    if (!f) throw new Error('handshake: not a frame');
    this.frame = f;
    this.challenge = await sha256(enc.encode(payload));
    const proof = await this.sign(concat(enc.encode('pcc-join-claim-v1'), enc.encode(payload), this.ecdh.pub));
    return { payload, e_v: b64url(this.ecdh.pub), sig_pub: b64url(this.sigPub), proof };
  }

  // call signs a later join call: "pcc-join-v1" ‖ hs ‖ sha256(frame) ‖ call.
  async call(name: JoinCall): Promise<string> {
    if (!this.frame || !this.challenge) throw new Error('handshake: not claimed');
    return this.sign(concat(enc.encode('pcc-join-v1'), this.frame.hs, this.challenge, enc.encode(name)));
  }

  async kmac(): Promise<Uint8Array> {
    if (!this.frame) throw new Error('handshake: not claimed');
    return kmac(this.ecdh, this.frame.ei, this.frame.hs);
  }

  async bits(k: Uint8Array, attempt: number): Promise<{ ultra: Uint8Array; jingle: Uint8Array }> {
    if (!this.frame) throw new Error('handshake: not claimed');
    return soundBits(k, transcript(this.frame.hs, this.frame.ei, this.ecdh.pub, this.frame.n, attempt));
  }
}

export type JoinCall = 'status' | 'audio_start' | 'dual_frame' | 'register_begin' | 'register_finish' | 'register_cosign';
