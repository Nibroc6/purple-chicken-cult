// A simulated acoustic channel for the codec's tests: a cheap phone speaker
// (2nd-order high-pass at 500 Hz, 1st-order low-pass at 5 kHz: about −12 dB
// at 19 kHz), white noise at a given SNR, leading and trailing room noise.
// Owner: P1-W13.

// rng is a seeded PRNG (mulberry32), so failures reproduce.
export function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function randomBytes(r: () => number, n: number): Uint8Array {
  return Uint8Array.from({ length: n }, () => Math.floor(r() * 256));
}

function gauss(r: () => number): number {
  const u = Math.max(r(), 1e-12);
  return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * r());
}

// speaker applies the speaker roll-off (in place on a copy).
export function speaker(x: Float32Array, rate: number): Float32Array {
  const out = new Float32Array(x.length);
  // 2nd-order Butterworth high-pass at 500 Hz (RBJ biquad).
  const w0 = (2 * Math.PI * 500) / rate;
  const alpha = Math.sin(w0) / Math.SQRT2;
  const cw = Math.cos(w0);
  const a0 = 1 + alpha;
  const b0 = (1 + cw) / 2 / a0;
  const b1 = -(1 + cw) / a0;
  const b2 = b0;
  const a1 = (-2 * cw) / a0;
  const a2 = (1 - alpha) / a0;
  let x1 = 0;
  let x2 = 0;
  let y1 = 0;
  let y2 = 0;
  // 1st-order low-pass at 5 kHz.
  const k = Math.exp((-2 * Math.PI * 5000) / rate);
  let lp = 0;
  for (let i = 0; i < x.length; i++) {
    const xi = x[i] ?? 0;
    const y = b0 * xi + b1 * x1 + b2 * x2 - a1 * y1 - a2 * y2;
    x2 = x1;
    x1 = xi;
    y2 = y1;
    y1 = y;
    lp = (1 - k) * y + k * lp;
    out[i] = lp;
  }
  return out;
}

function power(x: Float32Array): number {
  let s = 0;
  for (const v of x) s += v * v;
  return s / Math.max(1, x.length);
}

// channel plays sound through the speaker at gain, after `leadMs` of room
// noise and followed by `tailMs` more, with white noise at snrDb relative to
// the received sound's power.
export function channel(
  sound: Float32Array,
  rate: number,
  o: {
    snrDb: number;
    leadMs: number;
    tailMs?: number;
    gain?: number;
    seed: number;
  },
): Float32Array {
  const r = rng(o.seed);
  const heard = speaker(sound, rate);
  const gain = o.gain ?? 1;
  const sigma = Math.sqrt((power(heard) * gain * gain) / 10 ** (o.snrDb / 10));
  const lead = Math.round((o.leadMs * rate) / 1000);
  const out = new Float32Array(lead + heard.length + Math.round(((o.tailMs ?? 1000) * rate) / 1000));
  for (let i = 0; i < out.length; i++) {
    const s = i >= lead && i - lead < heard.length ? (heard[i - lead] ?? 0) * gain : 0;
    out[i] = s + sigma * gauss(r);
  }
  return out;
}
