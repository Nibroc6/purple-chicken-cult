// The handshake sound synthesizer: pure TypeScript, no Web Audio, so the same
// PCM plays on a device (createHandshakeAudio), renders offline into a WAV
// (wav.ts, the INT-1 fake microphone) and feeds the decoder in tests.
// Owner: P1-W13.
import type { PlayBits } from './index';
import {
  DATA_TONES,
  JINGLE_AMP,
  MOTIF,
  MOTIF_HZ,
  PREAMBLE,
  SCALE_HZ,
  SOUND_MS,
  SYMBOL_MS,
  TAIL,
  ULTRA_AMP,
  ULTRA_HZ,
  tailNotes,
  ultraSymbols,
  type Slot,
} from './layout';

export interface RenderOpts {
  quiet: boolean; // ultrasonic only (quiet mode)
  sampleRate: number;
}

// jingleFreqs are the 11 pitches of the jingle that carries jingle (motif first).
export function jingleFreqs(jingle: Uint8Array): number[] {
  return [...MOTIF_HZ, ...tailNotes(jingle).map((n) => SCALE_HZ[n] ?? 0)];
}

// ultraFreqs are the 20 tones of the ultrasonic layer that carries ultra.
export function ultraFreqs(ultra: Uint8Array): number[] {
  return [...PREAMBLE.map((t) => ULTRA_HZ[t]), ...ultraSymbols(ultra).map((s) => ULTRA_HZ[DATA_TONES[s] ?? 0])];
}

// checkBits rejects anything but 4 ultrasonic bytes and 3 jingle bytes.
export function checkBits(bits: PlayBits): void {
  if (bits.ultra.length !== 4 || bits.jingle.length !== 3) throw new Error('bad_bits');
}

// renderSound returns the mono PCM of one handshake sound (SOUND_MS long).
export function renderSound(bits: PlayBits, opts: RenderOpts): Float32Array<ArrayBuffer> {
  checkBits(bits);
  const rate = opts.sampleRate;
  const out = new Float32Array(Math.round((SOUND_MS * rate) / 1000));
  if (!opts.quiet) {
    const f = jingleFreqs(bits.jingle);
    [...MOTIF, ...TAIL].forEach((slot, i) => {
      note(out, rate, slot, f[i] ?? 0);
    });
  }
  // A device whose output cannot reach 18 kHz (rate below ~44 kHz) plays no ultrasonic layer.
  if ((ULTRA_HZ[ULTRA_HZ.length - 1] ?? 0) < 0.48 * rate) fsk(out, rate, ultraFreqs(bits.ultra));
  return out;
}

const envelopes = new Map<string, Float32Array>();

// envelope is a jingle note's amplitude over its n samples: a soft 6 ms
// attack, a gentle decay (a mallet-like tone) and a 12 ms raised-cosine
// release, so notes never click. The decoder uses it as its matched filter.
export function envelope(n: number, rate: number): Float32Array {
  const key = `${n}/${rate}`;
  let env = envelopes.get(key);
  if (!env) {
    env = new Float32Array(n);
    const attack = Math.round(0.006 * rate);
    const release = Math.round(0.012 * rate);
    for (let i = 0; i < n; i++) {
      let e = Math.exp(-i / (0.35 * rate));
      if (i < attack) e *= 0.5 - 0.5 * Math.cos((Math.PI * i) / attack);
      if (i > n - release) e *= 0.5 - 0.5 * Math.cos((Math.PI * (n - i)) / release);
      env[i] = e;
    }
    envelopes.set(key, env);
  }
  return env;
}

// note adds one jingle note.
function note(out: Float32Array, rate: number, slot: Slot, hz: number): void {
  const start = Math.round((slot.at * rate) / 1000);
  const n = Math.round((slot.dur * rate) / 1000);
  const env = envelope(n, rate);
  const w = (2 * Math.PI * hz) / rate;
  for (let i = 0; i < n && start + i < out.length; i++) {
    out[start + i] = (out[start + i] ?? 0) + JINGLE_AMP * (env[i] ?? 0) * Math.sin(w * i);
  }
}

// fsk adds the ultrasonic layer: continuous-phase FSK (no phase jumps between
// symbols, so no audible clicks), with 10 ms fades at both ends.
function fsk(out: Float32Array, rate: number, tones: number[]): void {
  const per = (SYMBOL_MS * rate) / 1000;
  const total = Math.round(tones.length * per);
  const fade = Math.round(0.01 * rate);
  let phase = 0;
  for (let i = 0; i < total && i < out.length; i++) {
    const hz = tones[Math.min(tones.length - 1, Math.floor(i / per))] ?? 0;
    phase += (2 * Math.PI * hz) / rate;
    if (phase > 2 * Math.PI) phase -= 2 * Math.PI;
    let env = 1;
    if (i < fade) env = 0.5 - 0.5 * Math.cos((Math.PI * i) / fade);
    if (i > total - fade) env = 0.5 - 0.5 * Math.cos((Math.PI * (total - i)) / fade);
    out[i] = (out[i] ?? 0) + ULTRA_AMP * env * Math.sin(phase);
  }
}
