// The handshake sound's fixed layout (ARCHITECTURE §7 "The handshake sound";
// sound design in docs/sound/README.md). Both layers start together at t = 0
// in one 1.5 s buffer:
//
//   jingle (audible, 659–2960 Hz): a fixed 3-note motif (E5 A5 B5), then 8
//     tail notes over a fixed rhythm, each one of an 8-tone scale (A major
//     pentatonic, C#6–F#7) = 3 bits per note, 24 bits.
//   ultrasonic (18.2–19.8 kHz): continuous-phase FSK, 20 symbols of 70 ms:
//     a 4-symbol preamble on the two outer tones, then 16 data symbols on the
//     four inner tones = 2 bits per symbol, 32 bits.
//
// The motif and preamble tones are never data tones, so a tail can never look
// like a motif and data can never look like a preamble. Owner: P1-W13.

export const SOUND_MS = 1500;

export interface Slot {
  at: number; // ms from the start of the sound
  dur: number; // ms
}

// The motif: E5 → A5 → B5, the same every time (the sound logo).
export const MOTIF_HZ = [659.26, 880.0, 987.77] as const;
export const MOTIF: readonly Slot[] = [
  { at: 0, dur: 100 },
  { at: 120, dur: 100 },
  { at: 240, dur: 160 },
];

// The tail's scale: A major pentatonic over 1.4 octaves, C#6 E6 F#6 A6 B6 C#7
// E7 F#7. Any sequence of these notes stays consonant, and all of them sit in
// the band cheap phone speakers reproduce well (above ~1 kHz, below 4 kHz).
export const SCALE_HZ = [1108.73, 1318.51, 1479.98, 1760.0, 1975.53, 2217.46, 2637.02, 2959.96] as const;

// The tail's fixed rhythm (short short long, short short short short long).
export const TAIL: readonly Slot[] = [
  { at: 440, dur: 90 },
  { at: 550, dur: 90 },
  { at: 660, dur: 150 },
  { at: 830, dur: 90 },
  { at: 940, dur: 90 },
  { at: 1050, dur: 90 },
  { at: 1160, dur: 90 },
  { at: 1270, dur: 200 },
];

// Ultrasonic tones, evenly spaced 320 Hz apart: 0 and 5 are the preamble's,
// 1–4 carry data.
export const ULTRA_HZ = [18200, 18520, 18840, 19160, 19480, 19800] as const;
export const DATA_TONES = [1, 2, 3, 4] as const;
export const PREAMBLE = [0, 5, 0, 5] as const;
export const SYMBOL_MS = 70;
export const DATA_SYMBOLS = 16;
export const ULTRA_MS = (PREAMBLE.length + DATA_SYMBOLS) * SYMBOL_MS;

// Peak amplitudes (full scale = 1). The sum stays below 0.8, so the mix never clips.
export const JINGLE_AMP = 0.5;
export const ULTRA_AMP = 0.22;

// Guard frequencies: the midpoints between neighbouring tones of a band and one
// step outside it. Their energy is the noise reference of the detectors.
function guards(tones: readonly number[]): number[] {
  const t = [...tones].sort((a, b) => a - b);
  const out: number[] = [];
  const first = t[0] ?? 0;
  const second = t[1] ?? first;
  out.push(first * (first / second) ** 0.5);
  for (let i = 1; i < t.length; i++) out.push(Math.sqrt((t[i - 1] ?? 0) * (t[i] ?? 0)));
  const last = t[t.length - 1] ?? 0;
  const prev = t[t.length - 2] ?? last;
  out.push(last * (last / prev) ** 0.5);
  return out;
}

export const JINGLE_GUARDS = guards([...MOTIF_HZ, ...SCALE_HZ]);
export const ULTRA_GUARDS = guards(ULTRA_HZ);

// toNotes splits bytes into n groups of `width` bits, most significant first.
export function toGroups(bytes: Uint8Array, width: number, n: number): number[] {
  const out: number[] = [];
  let acc = 0;
  let bits = 0;
  let i = 0;
  while (out.length < n) {
    if (bits < width) {
      acc = (acc << 8) | (bytes[i++] ?? 0);
      bits += 8;
    }
    bits -= width;
    out.push((acc >> bits) & ((1 << width) - 1));
    acc &= (1 << bits) - 1;
  }
  return out;
}

// fromGroups is the inverse of toGroups.
export function fromGroups(groups: readonly number[], width: number, nBytes: number): Uint8Array {
  const out = new Uint8Array(nBytes);
  let acc = 0;
  let bits = 0;
  let j = 0;
  for (const g of groups) {
    acc = (acc << width) | g;
    bits += width;
    while (bits >= 8 && j < nBytes) {
      bits -= 8;
      out[j++] = (acc >> bits) & 0xff;
      acc &= (1 << bits) - 1;
    }
  }
  return out;
}

// tailNotes are the 8 scale degrees (0–7) that carry 3 jingle bytes.
export function tailNotes(jingle: Uint8Array): number[] {
  return toGroups(jingle, 3, TAIL.length);
}

// ultraSymbols are the 16 data symbols (0–3) that carry 4 ultrasonic bytes.
export function ultraSymbols(ultra: Uint8Array): number[] {
  return toGroups(ultra, 2, DATA_SYMBOLS);
}
