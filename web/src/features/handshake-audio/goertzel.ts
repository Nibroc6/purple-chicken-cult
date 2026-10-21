// Goertzel tone detection over a Hann-windowed segment (ARCHITECTURE §7:
// "Decoding: Goertzel tone detection"). Owner: P1-W13.

const hanns = new Map<number, { w: Float32Array; sum: number }>();

function hann(n: number): { w: Float32Array; sum: number } {
  let h = hanns.get(n);
  if (!h) {
    const w = new Float32Array(n);
    let sum = 0;
    for (let i = 0; i < n; i++) {
      const v = 0.5 - 0.5 * Math.cos((2 * Math.PI * (i + 0.5)) / n);
      w[i] = v;
      sum += v;
    }
    h = { w, sum };
    hanns.set(n, h);
  }
  return h;
}

let scratch = new Float32Array(0);

// tonePowers returns, for each frequency, the squared amplitude of that tone
// in x[start, start+len) (a full-scale sine reads 1, silence 0). Samples
// outside x count as silence. The window is Hann, or `shape` (len samples):
// a note's own envelope makes it a matched filter, which peaks exactly where
// the note is.
export function tonePowers(
  x: Float32Array,
  start: number,
  len: number,
  freqs: readonly number[],
  rate: number,
  shape?: Float32Array,
): number[] {
  const { w, sum } = shape ? { w: shape, sum: shape.reduce((a, b) => a + b, 0) } : hann(len);
  if (scratch.length < len) scratch = new Float32Array(len);
  const seg = scratch;
  const from = Math.max(0, -start);
  const to = Math.min(len, x.length - start);
  seg.fill(0, 0, len);
  for (let i = from; i < to; i++) seg[i] = (x[start + i] as number) * (w[i] as number);
  const norm = 2 / sum;
  const out: number[] = [];
  // Four tones per pass: the four recurrences are independent, so they run in parallel.
  for (let f = 0; f < freqs.length; f += 4) {
    const c0 = 2 * Math.cos((2 * Math.PI * (freqs[f] ?? 0)) / rate);
    const c1 = 2 * Math.cos((2 * Math.PI * (freqs[f + 1] ?? 0)) / rate);
    const c2 = 2 * Math.cos((2 * Math.PI * (freqs[f + 2] ?? 0)) / rate);
    const c3 = 2 * Math.cos((2 * Math.PI * (freqs[f + 3] ?? 0)) / rate);
    let a1 = 0,
      a2 = 0,
      b1 = 0,
      b2 = 0,
      d1 = 0,
      d2 = 0,
      e1 = 0,
      e2 = 0;
    for (let i = 0; i < len; i++) {
      const v = seg[i] as number;
      const a0 = v + c0 * a1 - a2;
      const b0 = v + c1 * b1 - b2;
      const d0 = v + c2 * d1 - d2;
      const e0 = v + c3 * e1 - e2;
      a2 = a1;
      a1 = a0;
      b2 = b1;
      b1 = b0;
      d2 = d1;
      d1 = d0;
      e2 = e1;
      e1 = e0;
    }
    const n2 = norm * norm;
    out.push((a1 * a1 + a2 * a2 - c0 * a1 * a2) * n2);
    if (f + 1 < freqs.length) out.push((b1 * b1 + b2 * b2 - c1 * b1 * b2) * n2);
    if (f + 2 < freqs.length) out.push((d1 * d1 + d2 * d2 - c2 * d1 * d2) * n2);
    if (f + 3 < freqs.length) out.push((e1 * e1 + e2 * e2 - c3 * e1 * e2) * n2);
  }
  return out;
}
