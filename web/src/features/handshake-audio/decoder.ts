// The handshake sound decoder: Goertzel tone detection for the jingle, FSK
// demodulation for the ultrasonic layer, each with its own sync (the motif,
// the preamble), so each layer decodes on its own and the offset between them
// is measured. Streaming: samples arrive in chunks; the first motif or
// preamble anchors the search and the decoder answers once the whole sound
// (and the search window around it) has arrived. Samples live in memory only
// for one listen; nothing is recorded or kept. Owner: P1-W13.
import type { DecodeReport } from './index';
import {
  DATA_SYMBOLS,
  DATA_TONES,
  JINGLE_GUARDS,
  MOTIF,
  MOTIF_HZ,
  PREAMBLE,
  SCALE_HZ,
  SOUND_MS,
  SYMBOL_MS,
  TAIL,
  ULTRA_GUARDS,
  ULTRA_HZ,
  fromGroups,
} from './layout';
import { tonePowers } from './goertzel';
import { envelope } from './synth';

const HOP_MS = 10;
const WIN_MS = 40;
// SEARCH_MS: once one layer is heard, the other is looked for this far either side.
const SEARCH_MS = 400;
// REFINE_MS: the fine sync search either side of the coarse one, in 1 ms steps.
const REFINE_MS = 30;
// Power floor (−85 dB re a full-scale sine): quieter tones are not heard.
const FLOOR = 10 ** (-85 / 10);
// A motif or preamble is heard when, on average, this share of its band's
// energy sits in the expected tone (each slot at least MIN_SLOT).
const DETECT = 0.4;
const MIN_SLOT = 0.2;
// The motif level reported when nothing is heard at all.
const SILENT_DB = -120;

// Below this motif level the inviter's device asks for the volume to be turned
// up (ARCHITECTURE §7 "Volume reminder").
export const VOLUME_LOW_DB = -50;

// volumeLow: the motif was heard faintly or not at all.
export function volumeLow(r: DecodeReport): boolean {
  return !Number.isFinite(r.motifLevelDb) || r.motifLevelDb < VOLUME_LOW_DB;
}

const JINGLE_BAND = [...MOTIF_HZ, ...SCALE_HZ, ...JINGLE_GUARDS];
const ULTRA_BAND = [...ULTRA_HZ, ...ULTRA_GUARDS];
const GRID = [...JINGLE_BAND, ...ULTRA_BAND];
const U0 = JINGLE_BAND.length; // grid index of ULTRA_HZ[0]

interface Sync {
  tones: number[]; // grid index per slot
  hops: number[]; // coarse window start per slot, in hops from the sound's start
  band: 0 | 1; // 0 jingle, 1 ultrasonic
}

function coarse(at: number, dur: number): number {
  return Math.round((at + dur / 2 - WIN_MS / 2) / HOP_MS);
}

const MOTIF_SYNC: Sync = {
  tones: MOTIF.map((_, i) => i),
  hops: MOTIF.map((s) => coarse(s.at, s.dur)),
  band: 0,
};
const PRE_SYNC: Sync = {
  tones: PREAMBLE.map((t) => U0 + t),
  hops: PREAMBLE.map((_, j) => coarse(j * SYMBOL_MS, SYMBOL_MS)),
  band: 1,
};
const SPAN = Math.max(...MOTIF_SYNC.hops, ...PRE_SYNC.hops);

const db = (p: number) => Math.max(SILENT_DB, 10 * Math.log10(Math.max(p, 1e-30)));

export class StreamDecoder {
  readonly rate: number;
  private readonly hop: number;
  private readonly win: number;
  private buf: Float32Array;
  private n = 0;
  // Per hop: the grid powers, then the jingle band's sum and the ultrasonic band's.
  private readonly rows: Float32Array[] = [];
  private readonly motif: number[] = [];
  private readonly pre: number[] = [];
  private anchor: number | undefined;
  private loudest = 0; // the loudest motif candidate so far (power)

  constructor(rate: number) {
    this.rate = rate;
    this.hop = Math.round((HOP_MS * rate) / 1000);
    this.win = Math.round((WIN_MS * rate) / 1000);
    this.buf = new Float32Array(Math.round(rate * 4));
  }

  // ms is how much audio has arrived, in milliseconds.
  get ms(): number {
    return (this.n * 1000) / this.rate;
  }

  // push appends samples; it returns the report once a sound has been decoded.
  push(chunk: Float32Array): DecodeReport | undefined {
    if (this.n + chunk.length > this.buf.length) {
      const grown = new Float32Array(Math.max(this.buf.length * 2, this.n + chunk.length));
      grown.set(this.buf.subarray(0, this.n));
      this.buf = grown;
    }
    this.buf.set(chunk, this.n);
    this.n += chunk.length;
    while (this.rows.length * this.hop + this.win <= this.n) this.addRow();
    if (this.anchor !== undefined && this.n >= this.needed(this.anchor)) return this.decode();
    return undefined;
  }

  // finish decodes whatever has arrived (the listen timed out or the input ended).
  finish(): DecodeReport {
    if (this.anchor !== undefined) return this.decode();
    return { motifLevelDb: db(this.loudest) };
  }

  private needed(anchor: number): number {
    return anchor * this.hop + Math.round(((SEARCH_MS + SOUND_MS + REFINE_MS + HOP_MS) * this.rate) / 1000);
  }

  private addRow(): void {
    const p = tonePowers(this.buf, this.rows.length * this.hop, this.win, GRID, this.rate);
    const row = new Float32Array(GRID.length + 2);
    row.set(p);
    let j = 0;
    let u = 0;
    for (let i = 0; i < GRID.length; i++) {
      if (i < U0) j += p[i] ?? 0;
      else u += p[i] ?? 0;
    }
    row[GRID.length] = j;
    row[GRID.length + 1] = u;
    this.rows.push(row);
    const s = this.rows.length - 1 - SPAN;
    if (s < 0) return;
    const m = this.score(MOTIF_SYNC, s);
    const q = this.score(PRE_SYNC, s);
    this.motif.push(m);
    this.pre.push(q);
    let level = 0;
    MOTIF_SYNC.hops.forEach((h, i) => (level += this.rows[s + h]?.[i] ?? 0));
    this.loudest = Math.max(this.loudest, level / MOTIF.length);
    if (this.anchor === undefined && (m >= DETECT || q >= DETECT)) this.anchor = s;
  }

  // score is the sync's mean in-tone share at coarse start s (0 when any slot is below MIN_SLOT).
  private score(sync: Sync, s: number): number {
    let sum = 0;
    for (let i = 0; i < sync.tones.length; i++) {
      const row = this.rows[s + (sync.hops[i] ?? 0)];
      if (!row) return 0;
      const r = (row[sync.tones[i] ?? 0] ?? 0) / ((row[GRID.length + sync.band] ?? 0) + FLOOR);
      if (r < MIN_SLOT) return 0;
      sum += r;
    }
    return sum / sync.tones.length;
  }

  // best is the coarse start with the highest score in [lo, hi], if any is heard.
  private best(scores: number[], lo: number, hi: number): number | undefined {
    let at: number | undefined;
    let top = DETECT;
    for (let s = Math.max(0, lo); s <= hi && s < scores.length; s++) {
      const v = scores[s] ?? 0;
      if (v >= top) {
        top = v;
        at = s;
      }
    }
    return at;
  }

  private samples(ms: number): number {
    return Math.round((ms * this.rate) / 1000);
  }

  // refine finds the sample where the sync's tones line up best, near coarse start s.
  private refine(sync: 'motif' | 'pre', s: number): number {
    const slots =
      sync === 'motif'
        ? MOTIF.map((m, i) => ({ at: m.at, dur: m.dur, hz: MOTIF_HZ[i] ?? 0 }))
        : PREAMBLE.map((t, j) => ({
            at: j * SYMBOL_MS,
            dur: SYMBOL_MS,
            hz: ULTRA_HZ[t],
          }));
    const c = s * this.hop;
    const step = Math.max(1, this.samples(1));
    let bestAt = c;
    let top = -1;
    for (let o = c - this.samples(REFINE_MS); o <= c + this.samples(REFINE_MS); o += step) {
      let t = 0;
      for (const sl of slots) {
        const n = this.samples(sl.dur);
        const shape = sync === 'motif' ? envelope(n, this.rate) : undefined;
        t += tonePowers(this.buf, o + this.samples(sl.at), n, [sl.hz], this.rate, shape)[0] ?? 0;
      }
      if (t > top) {
        top = t;
        bestAt = o;
      }
    }
    return bestAt;
  }

  // argmax is the strongest of freqs in the window [at, at+len).
  private argmax(at: number, len: number, freqs: readonly number[]): number {
    const p = tonePowers(this.buf, at, len, freqs, this.rate);
    let k = 0;
    p.forEach((v, i) => {
      if (v > (p[k] ?? 0)) k = i;
    });
    return k;
  }

  private motifLevel(start: number): number {
    let sum = 0;
    MOTIF.forEach((m, i) => {
      sum +=
        tonePowers(this.buf, start + this.samples(m.at + 10), this.samples(m.dur - 20), [MOTIF_HZ[i] ?? 0], this.rate)[0] ?? 0;
    });
    return db(sum / MOTIF.length);
  }

  private decode(): DecodeReport {
    const anchor = this.anchor ?? 0;
    const lo = anchor - Math.round(SEARCH_MS / HOP_MS);
    const hi = anchor + Math.round(SEARCH_MS / HOP_MS);
    const m = this.best(this.motif, lo, hi);
    const q = this.best(this.pre, lo, hi);
    const rep: DecodeReport = { motifLevelDb: SILENT_DB };
    const toMs = (samples: number) => (samples * 1000) / this.rate;
    let jStart: number | undefined;
    let uStart: number | undefined;
    if (m !== undefined) {
      const at = this.refine('motif', m);
      const notes = TAIL.map((t) => this.argmax(at + this.samples(t.at + 15), this.samples(t.dur - 30), SCALE_HZ));
      rep.jingleBits = fromGroups(notes, 3, 3);
      rep.tJingleMs = toMs(this.n - at);
      jStart = at;
    }
    if (q !== undefined) {
      const at = this.refine('pre', q);
      const syms: number[] = [];
      const data = DATA_TONES.map((t) => ULTRA_HZ[t]);
      for (let k = 0; k < DATA_SYMBOLS; k++) {
        syms.push(this.argmax(at + this.samples((PREAMBLE.length + k) * SYMBOL_MS + 10), this.samples(SYMBOL_MS - 20), data));
      }
      rep.ultraBits = fromGroups(syms, 2, 4);
      rep.tUltraMs = toMs(this.n - at);
      uStart = at;
    }
    if (jStart !== undefined && uStart !== undefined) rep.offsetMs = toMs(jStart - uStart);
    // The motif's level where it was heard, or where it should have been (quiet mode, a faint jingle).
    const at = jStart ?? uStart;
    if (at !== undefined) rep.motifLevelDb = this.motifLevel(at);
    this.anchor = undefined;
    return rep;
  }
}

// decodePcm decodes one buffer of PCM (tests, a WAV): the first sound in it.
export function decodePcm(pcm: Float32Array, rate: number): DecodeReport {
  const d = new StreamDecoder(rate);
  const chunk = 4096;
  for (let i = 0; i < pcm.length; i += chunk) {
    const r = d.push(pcm.subarray(i, i + chunk));
    if (r) return r;
  }
  return d.finish();
}
