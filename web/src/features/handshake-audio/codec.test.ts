// The codec's acceptance tests (plan P1-W13): round trip through a speaker
// roll-off at SNR 10 dB (> 99% jingle, > 95% ultrasonic), the sound fits in
// 1.6 s, and 1,000 random tails all stay within the scale. Owner: P1-W13.
import { describe, expect, it } from 'vitest';
import { channel, randomBytes, rng } from './channel.test-util';
import { createHandshakeAudio, listenTo } from './createHandshakeAudio';
import { VOLUME_LOW_DB, decodePcm, volumeLow } from './decoder';
import { MOTIF_HZ, SCALE_HZ, SOUND_MS, ULTRA_HZ, fromGroups, tailNotes, toGroups } from './layout';
import { jingleFreqs, renderSound, ultraFreqs } from './synth';
import { decodeWav, encodeWav, pcmInput, soundWav, wavInput } from './wav';

const eq = (a: Uint8Array | undefined, b: Uint8Array) => !!a && a.length === b.length && a.every((v, i) => v === b[i]);

describe('round trip', () => {
  for (const rate of [48000, 44100]) {
    it(`decodes both layers at SNR 10 dB through a speaker roll-off (${rate} Hz)`, () => {
      const r = rng(rate);
      const trials = 150;
      let jingle = 0;
      let ultra = 0;
      for (let i = 0; i < trials; i++) {
        const bits = { ultra: randomBytes(r, 4), jingle: randomBytes(r, 3) };
        const pcm = channel(renderSound(bits, { quiet: false, sampleRate: rate }), rate, {
          snrDb: 10,
          leadMs: 100 + r() * 900,
          seed: i,
        });
        const rep = decodePcm(pcm, rate);
        if (eq(rep.jingleBits, bits.jingle)) jingle++;
        if (eq(rep.ultraBits, bits.ultra)) ultra++;
      }
      expect(jingle / trials).toBeGreaterThan(0.99);
      expect(ultra / trials).toBeGreaterThan(0.95);
    }, 120_000);
  }
});

describe('layout', () => {
  it('fits in 1.6 s', () => {
    expect(SOUND_MS).toBeLessThanOrEqual(1600);
    const pcm = renderSound({ ultra: new Uint8Array(4), jingle: new Uint8Array(3) }, { quiet: false, sampleRate: 48000 });
    expect(pcm.length / 48000).toBeLessThanOrEqual(1.6);
  });

  it('keeps 1,000 random tails within the scale', () => {
    const r = rng(7);
    for (let i = 0; i < 1000; i++) {
      const f = jingleFreqs(randomBytes(r, 3)).slice(3);
      expect(f).toHaveLength(8);
      for (const hz of f) expect(SCALE_HZ).toContain(hz);
    }
  });

  it('round-trips bit groups', () => {
    const b = Uint8Array.of(0xa5, 0x3c, 0xff);
    expect(fromGroups(tailNotes(b), 3, 3)).toEqual(b);
    const u = Uint8Array.of(1, 2, 3, 250);
    expect(fromGroups(toGroups(u, 2, 16), 2, 4)).toEqual(u);
  });
});

const BITS = {
  ultra: Uint8Array.of(0xde, 0xad, 0xbe, 0xef),
  jingle: Uint8Array.of(0x12, 0x34, 0x56),
};
const RATE = 48000;

// jingleOnly is the sound without its ultrasonic layer.
function jingleOnly(): Float32Array {
  const quiet = renderSound(BITS, { quiet: true, sampleRate: RATE });
  return renderSound(BITS, { quiet: false, sampleRate: RATE }).map((v, i) => v - (quiet[i] ?? 0));
}

const clean = (quiet: boolean, leadMs = 300) =>
  channel(renderSound(BITS, { quiet, sampleRate: RATE }), RATE, {
    snrDb: 30,
    leadMs,
    seed: 1,
  });

// bandShare is the share of x's energy within [lo, hi] Hz (a plain DFT of its
// first 8192 samples, every other bin).
function bandShare(x: Float32Array, rate: number, lo: number, hi: number): number {
  const seg = x.subarray(0, 8192);
  let inBand = 0;
  let all = 0;
  for (let k = 1; k < seg.length / 2; k += 2) {
    const hz = (k * rate) / seg.length;
    let re = 0;
    let im = 0;
    const w = (2 * Math.PI * k) / seg.length;
    for (let i = 0; i < seg.length; i += 1) {
      re += (seg[i] ?? 0) * Math.cos(w * i);
      im -= (seg[i] ?? 0) * Math.sin(w * i);
    }
    const p = re * re + im * im;
    all += p;
    if (hz >= lo && hz <= hi) inBand += p;
  }
  return inBand / all;
}

describe('sound design', () => {
  it('keeps the jingle within 400–4000 Hz and the ultrasonic layer within 18–20 kHz', () => {
    for (const hz of [...MOTIF_HZ, ...SCALE_HZ]) {
      expect(hz).toBeGreaterThanOrEqual(400);
      expect(hz).toBeLessThanOrEqual(4000);
    }
    for (const hz of ULTRA_HZ) {
      expect(hz).toBeGreaterThanOrEqual(18000);
      expect(hz).toBeLessThanOrEqual(20000);
    }
    expect(new Set(ultraFreqs(BITS.ultra).slice(0, 4))).toEqual(new Set([ULTRA_HZ[0], ULTRA_HZ[5]]));
    expect(bandShare(jingleOnly(), RATE, 400, 4000)).toBeGreaterThan(0.99);
    expect(bandShare(renderSound(BITS, { quiet: true, sampleRate: RATE }), RATE, 18000, 20000)).toBeGreaterThan(0.99);
  });

  it('never clips', () => {
    const r = rng(3);
    for (let i = 0; i < 50; i++) {
      const pcm = renderSound({ ultra: randomBytes(r, 4), jingle: randomBytes(r, 3) }, { quiet: false, sampleRate: RATE });
      expect(Math.max(...pcm.map(Math.abs))).toBeLessThan(0.8);
    }
  });

  it('rejects bits of the wrong length', () => {
    expect(() =>
      renderSound({ ultra: new Uint8Array(3), jingle: new Uint8Array(3) }, { quiet: false, sampleRate: RATE }),
    ).toThrow();
  });

  it('plays no ultrasonic layer when the output cannot reach it', () => {
    const pcm = renderSound(BITS, { quiet: true, sampleRate: 32000 });
    expect(pcm.every((v) => v === 0)).toBe(true);
  });
});

describe('decoder', () => {
  it('decodes both layers in sync and reports the motif level and timings', () => {
    const rep = decodePcm(clean(false), RATE);
    expect(rep.jingleBits).toEqual(BITS.jingle);
    expect(rep.ultraBits).toEqual(BITS.ultra);
    expect(Math.abs(rep.offsetMs ?? 1e9)).toBeLessThanOrEqual(5);
    expect(rep.tJingleMs).toBeGreaterThan(SOUND_MS - 100);
    expect(rep.tJingleMs).toBeLessThan(2500);
    expect(rep.tUltraMs).toBeLessThan(2500);
    expect(rep.motifLevelDb).toBeGreaterThan(-12);
    expect(volumeLow(rep)).toBe(false);
  });

  it('decodes quiet mode (ultrasonic only) and reports the missing motif as low', () => {
    const rep = decodePcm(clean(true), RATE);
    expect(rep.ultraBits).toEqual(BITS.ultra);
    expect(rep.jingleBits).toBeUndefined();
    expect(rep.offsetMs).toBeUndefined();
    expect(volumeLow(rep)).toBe(true);
  });

  it('flags a faint jingle as volume low and still decodes it', () => {
    const pcm = channel(renderSound(BITS, { quiet: false, sampleRate: RATE }), RATE, {
      snrDb: 20,
      leadMs: 200,
      gain: 0.002,
      seed: 2,
    });
    const rep = decodePcm(pcm, RATE);
    expect(rep.jingleBits).toEqual(BITS.jingle);
    expect(rep.motifLevelDb).toBeLessThan(VOLUME_LOW_DB);
    expect(volumeLow(rep)).toBe(true);
  });

  it('measures the offset when one layer arrives late (a relay that passes only one band)', () => {
    const jingle = jingleOnly();
    const ultra = renderSound(BITS, { quiet: true, sampleRate: RATE });
    const late = Math.round(0.25 * RATE);
    const mix = new Float32Array(RATE * 3);
    jingle.forEach((v, i) => (mix[RATE / 4 + late + i] = v));
    ultra.forEach((v, i) => (mix[RATE / 4 + i] = (mix[RATE / 4 + i] ?? 0) + v));
    const rep = decodePcm(mix, RATE);
    expect(rep.jingleBits).toEqual(BITS.jingle);
    expect(rep.ultraBits).toEqual(BITS.ultra);
    expect(rep.offsetMs).toBeGreaterThan(245);
    expect(rep.offsetMs).toBeLessThan(255);
  });

  it('hears nothing in noise or silence', () => {
    for (let seed = 0; seed < 10; seed++) {
      const r = rng(seed);
      const noise = Float32Array.from({ length: RATE * 3 }, () => (r() - 0.5) * 0.2);
      const rep = decodePcm(noise, RATE);
      expect(rep.jingleBits).toBeUndefined();
      expect(rep.ultraBits).toBeUndefined();
    }
    const rep = decodePcm(new Float32Array(RATE * 3), RATE);
    expect(rep).toEqual({ motifLevelDb: -120 });
    expect(volumeLow(rep)).toBe(true);
  });
});

describe('listen', () => {
  it('stops as soon as a sound is decoded, reading the input in chunks', async () => {
    const pcm = clean(false, 2000);
    let reads = 0;
    const input = pcmInput(pcm, RATE);
    const rep = await listenTo({ read: (n) => (reads++, input.read(n)) }, RATE, 15_000);
    expect(rep.ultraBits).toEqual(BITS.ultra);
    expect(rep.jingleBits).toEqual(BITS.jingle);
    expect(reads * 4096).toBeLessThan(pcm.length);
  });

  it('ends at the end of the input, and at the timeout', async () => {
    const silent = pcmInput(new Float32Array(RATE), RATE);
    expect(await listenTo(silent, RATE, 15_000)).toEqual({
      motifLevelDb: -120,
    });
    const forever = { read: () => new Promise<Float32Array>(() => undefined) };
    const t0 = performance.now();
    expect(await listenTo(forever, RATE, 50)).toEqual({ motifLevelDb: -120 });
    expect(performance.now() - t0).toBeLessThan(1000);
    const endless = {
      read: (n: number) => Promise.resolve(new Float32Array(n)),
    };
    expect((await listenTo(endless, RATE, 2000)).ultraBits).toBeUndefined();
  });

  it("honours listen({input}) and the input's own sample rate", async () => {
    const ctx = { sampleRate: 96000 } as AudioContext;
    const wav = soundWav(BITS, { quiet: false, sampleRate: 44100 });
    const rep = await createHandshakeAudio(ctx).listen({
      timeoutMs: 15_000,
      input: wavInput(wav),
    });
    expect(rep.jingleBits).toEqual(BITS.jingle);
    expect(rep.ultraBits).toEqual(BITS.ultra);
  });
});

describe('wav', () => {
  it('round-trips 16-bit PCM and reads float WAVs', () => {
    const pcm = Float32Array.of(0, 0.5, -0.5, 1, -1);
    const w = decodeWav(encodeWav(pcm, 22050));
    expect(w.sampleRate).toBe(22050);
    w.pcm.forEach((v, i) => {
      expect(v).toBeCloseTo(pcm[i] ?? 0, 3);
    });
    const f = new Uint8Array(44 + 8);
    f.set(encodeWav(new Float32Array(0), 8000).subarray(0, 44));
    const v = new DataView(f.buffer);
    v.setUint16(20, 3, true);
    v.setUint16(22, 2, true);
    v.setUint16(34, 32, true);
    v.setUint32(40, 8, true);
    v.setFloat32(44, 0.25, true);
    v.setFloat32(48, -1, true);
    expect(Array.from(decodeWav(f).pcm)).toEqual([0.25]);
    expect(() => decodeWav(new Uint8Array(10))).toThrow();
  });

  it('renders a sound into a WAV that decodes', () => {
    const w = decodeWav(soundWav(BITS, { quiet: false, leadMs: 700 }));
    expect(w.sampleRate).toBe(48000);
    const rep = decodePcm(w.pcm, w.sampleRate);
    expect(rep.ultraBits).toEqual(BITS.ultra);
    expect(rep.jingleBits).toEqual(BITS.jingle);
  });
});
