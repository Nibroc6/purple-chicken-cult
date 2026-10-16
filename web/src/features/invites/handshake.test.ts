// The device side of the handshake protocol against the vectors that
// internal/gate/invites/hstest pins for the server (TestKnownAnswers), and the
// two devices against each other.
import { describe, expect, it } from 'vitest';
import {
  b64url,
  ClaimDevice,
  commit,
  ephemeral,
  frameAt,
  fromB64url,
  isDualFrame,
  isFrame,
  kmac,
  kmacFromShared,
  parseFrame,
  renderDualFrame,
  renderFrame,
  sha256,
  soundBits,
  transcript,
  uuidBytes,
} from './handshake';

const hex = (u: Uint8Array) => Array.from(u, (b) => b.toString(16).padStart(2, '0')).join('');
const fill = (n: number, v: number) => new Uint8Array(n).fill(v);

describe('handshake protocol vectors', () => {
  const key = fill(32, 1);
  const hs = uuidBytes('00112233-4455-6677-8899-aabbccddeeff');
  const ei = Uint8Array.of(4, ...fill(64, 2));
  const ev = Uint8Array.of(4, ...fill(64, 3));

  it('renders frames like the server', async () => {
    const p = await renderFrame(key, { hs, ei, n: 7 });
    expect(p).toBe(
      'pcc2.ABEiM0RVZneImaq7zN3u_w.BAICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI.7.8KF1C2IPRjHzFw',
    );
    expect(isFrame(p)).toBe(true);
    const f = parseFrame(p);
    expect(f && hex(f.hs)).toBe(hex(hs));
    expect(f?.n).toBe(7);
    expect(parseFrame('pcc1.AAAAAAAAAAAAAAAAAAAAAA.1.BBBBBBBBBBBBBB')).toBeUndefined();
  });

  it('derives kmac, the sound bits and the dual frame like the server', async () => {
    const k = await kmacFromShared(fill(32, 5), hs);
    expect(hex(k)).toBe('3c19db04145a9ab3acef340b07b6332351bddb6c8e5927db77701ed5679912f0');
    const bits = await soundBits(k, transcript(hs, ei, ev, 7, 2));
    expect(hex(bits.ultra)).toBe('382cd90a');
    expect(hex(bits.jingle)).toBe('d8eb15');
    const dual = await renderDualFrame(k, hs, 9);
    expect(dual).toBe('pcc2d.ABEiM0RVZneImaq7zN3u_w.9.DGeVNv_QXHUySQ');
    expect(isDualFrame(dual)).toBe(true);
    expect(await commit(k)).toBe(b64url(await sha256(k)));
  });

  it('counts frames from the synced one', () => {
    expect(frameAt(10, 1000, 1000)).toBe(10);
    expect(frameAt(10, 1000, 1249)).toBe(10);
    expect(frameAt(10, 1000, 1250)).toBe(11);
    expect(frameAt(10, 1000, 900)).toBe(10);
  });

  it('round-trips base64url', () => {
    const u = Uint8Array.of(0, 250, 251, 252, 253, 254, 255);
    expect(b64url(u)).toBe('APr7_P3-_w');
    expect(Array.from(fromB64url(b64url(u)))).toEqual(Array.from(u));
    expect(() => fromB64url('a+b')).toThrow();
  });
});

describe('the two devices', () => {
  it('derive the same kmac and the claim carries a verifiable proof', async () => {
    const ini = await ephemeral();
    const frame = await renderFrame(fill(32, 9), { hs: fill(16, 7), ei: ini.pub, n: 3 });
    const dev = await ClaimDevice.create();
    const body = await dev.claim(frame);
    expect(body.payload).toBe(frame);
    expect(fromB64url(body.e_v)).toHaveLength(65);
    expect(fromB64url(body.proof)).toHaveLength(64); // P1363 r ‖ s
    const pub = await crypto.subtle.importKey(
      'raw',
      fromB64url(body.sig_pub),
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['verify'],
    );
    const msg = new TextEncoder().encode(`pcc-join-claim-v1${frame}`);
    const ok = await crypto.subtle.verify(
      { name: 'ECDSA', hash: 'SHA-256' },
      pub,
      fromB64url(body.proof),
      Uint8Array.of(...msg, ...fromB64url(body.e_v)),
    );
    expect(ok).toBe(true);
    const mine = await dev.kmac();
    const theirs = await kmac(ini, fromB64url(body.e_v), fill(16, 7));
    expect(hex(mine)).toBe(hex(theirs));
    expect(fromB64url(await dev.call('status'))).toHaveLength(64);
    const bits = await dev.bits(mine, 1);
    expect(bits.ultra).toHaveLength(4);
    expect(bits.jingle).toHaveLength(3);
  });
});
