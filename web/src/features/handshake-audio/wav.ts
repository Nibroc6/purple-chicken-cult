// WAV in and out (plan §7 "P1 audio"): INT-1's real fake-audio journey plays a
// WAV of the handshake sound into Chromium's fake microphone
// (--use-file-for-fake-audio-capture), and wavInput turns a WAV into an
// AudioInput for listen(). Mono 16-bit PCM out; 16-bit, 32-bit float and
// any channel count in (the first channel is used). Owner: P1-W13.
import type { AudioInput, PlayBits } from './index';
import { renderSound } from './synth';

// encodeWav packs mono PCM (−1…1) as a 16-bit WAV.
export function encodeWav(pcm: Float32Array, sampleRate: number): Uint8Array {
  const out = new Uint8Array(44 + pcm.length * 2);
  const v = new DataView(out.buffer);
  const tag = (at: number, s: string) => {
    for (let i = 0; i < 4; i++) v.setUint8(at + i, s.charCodeAt(i));
  };
  tag(0, 'RIFF');
  v.setUint32(4, 36 + pcm.length * 2, true);
  tag(8, 'WAVE');
  tag(12, 'fmt ');
  v.setUint32(16, 16, true);
  v.setUint16(20, 1, true); // PCM
  v.setUint16(22, 1, true); // mono
  v.setUint32(24, sampleRate, true);
  v.setUint32(28, sampleRate * 2, true);
  v.setUint16(32, 2, true);
  v.setUint16(34, 16, true);
  tag(36, 'data');
  v.setUint32(40, pcm.length * 2, true);
  pcm.forEach((s, i) => {
    v.setInt16(44 + i * 2, Math.round(Math.max(-1, Math.min(1, s)) * 32767), true);
  });
  return out;
}

export interface Wav {
  pcm: Float32Array;
  sampleRate: number;
}

// decodeWav reads the first channel of a PCM16 or float32 WAV.
export function decodeWav(bytes: Uint8Array): Wav {
  const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const tag = (at: number) => String.fromCharCode(...bytes.subarray(at, at + 4));
  if (bytes.length < 12 || tag(0) !== 'RIFF' || tag(8) !== 'WAVE') throw new Error('bad_wav');
  let format = 0;
  let channels = 0;
  let sampleRate = 0;
  let bitsPer = 0;
  for (let at = 12; at + 8 <= bytes.length; ) {
    const id = tag(at);
    const size = v.getUint32(at + 4, true);
    const body = at + 8;
    if (id === 'fmt ') {
      format = v.getUint16(body, true);
      channels = v.getUint16(body + 2, true);
      sampleRate = v.getUint32(body + 4, true);
      bitsPer = v.getUint16(body + 14, true);
    } else if (id === 'data') {
      const width = bitsPer / 8;
      const frame = width * channels;
      if (!frame || !((format === 1 && bitsPer === 16) || (format === 3 && bitsPer === 32))) throw new Error('bad_wav');
      const n = Math.floor(Math.min(size, bytes.length - body) / frame);
      const pcm = new Float32Array(n);
      for (let i = 0; i < n; i++) {
        const o = body + i * frame;
        pcm[i] = format === 1 ? v.getInt16(o, true) / 32768 : v.getFloat32(o, true);
      }
      return { pcm, sampleRate };
    }
    at = body + size + (size & 1);
  }
  throw new Error('bad_wav');
}

// A PcmInput is an AudioInput that knows its sample rate (listen() uses it in
// place of the AudioContext's).
export interface PcmInput extends AudioInput {
  sampleRate: number;
}

// pcmInput feeds pcm to listen() in chunks; past the end it returns an empty
// chunk, which ends the listen.
export function pcmInput(pcm: Float32Array, sampleRate: number): PcmInput {
  let at = 0;
  return {
    sampleRate,
    read: (frames: number) => {
      const out = pcm.slice(at, at + frames);
      at += out.length;
      return Promise.resolve(out);
    },
  };
}

// wavInput is the AudioInput of a WAV file.
export function wavInput(bytes: Uint8Array): PcmInput {
  const w = decodeWav(bytes);
  return pcmInput(w.pcm, w.sampleRate);
}

// soundWav renders one handshake sound as a WAV, with leadMs of silence
// before it and tailMs after (the fake microphone loops the file).
export function soundWav(
  bits: PlayBits,
  opts: {
    quiet: boolean;
    sampleRate?: number;
    leadMs?: number;
    tailMs?: number;
  },
): Uint8Array {
  const rate = opts.sampleRate ?? 48000;
  const sound = renderSound(bits, { quiet: opts.quiet, sampleRate: rate });
  const lead = Math.round(((opts.leadMs ?? 500) * rate) / 1000);
  const pcm = new Float32Array(lead + sound.length + Math.round(((opts.tailMs ?? 1500) * rate) / 1000));
  pcm.set(sound, lead);
  return encodeWav(pcm, rate);
}
