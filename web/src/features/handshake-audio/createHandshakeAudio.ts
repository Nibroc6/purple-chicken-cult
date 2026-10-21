// createHandshakeAudio (features/handshake-audio contract). Owner: P1-W13.
// play renders the sound (synth.ts) into an AudioBuffer and plays it once;
// listen decodes the microphone, or opts.input when given (the AudioInput test
// seam), until a sound is decoded, the input ends or timeoutMs passes.
import type { AudioInput, DecodeReport, HandshakeAudio, PlayBits } from './index';
import { StreamDecoder } from './decoder';
import { openMic } from './mic';
import { checkBits, renderSound } from './synth';

const CHUNK = 4096;

export function createHandshakeAudio(ctx: AudioContext): HandshakeAudio {
  return {
    play: async (bits: PlayBits, opts: { quiet: boolean }) => {
      checkBits(bits);
      if (ctx.state === 'suspended') await ctx.resume();
      const pcm = renderSound(bits, {
        quiet: opts.quiet,
        sampleRate: ctx.sampleRate,
      });
      const buf = ctx.createBuffer(1, pcm.length, ctx.sampleRate);
      buf.copyToChannel(pcm, 0);
      const src = ctx.createBufferSource();
      src.buffer = buf;
      src.connect(ctx.destination);
      return new Promise<{ startedAt: number }>((resolve) => {
        const startedAt = performance.now();
        src.onended = () => {
          src.disconnect();
          resolve({ startedAt });
        };
        src.start();
      });
    },
    listen: async (opts: { timeoutMs: number; input?: AudioInput }) => {
      if (opts.input) return listenTo(opts.input, rateOf(opts.input) ?? ctx.sampleRate, opts.timeoutMs);
      const mic = await openMic(ctx);
      try {
        return await listenTo(mic, mic.sampleRate, opts.timeoutMs);
      } finally {
        mic.close();
      }
    },
  };
}

// rateOf is an input's own sample rate, when it has one (a PcmInput).
function rateOf(input: AudioInput): number | undefined {
  const r = (input as { sampleRate?: unknown }).sampleRate;
  return typeof r === 'number' && r > 0 ? r : undefined;
}

// listenTo reads input until a sound decodes, the input ends (an empty chunk),
// timeoutMs of audio has arrived or timeoutMs of wall time has passed.
export async function listenTo(input: AudioInput, rate: number, timeoutMs: number): Promise<DecodeReport> {
  const dec = new StreamDecoder(rate);
  const deadline = performance.now() + timeoutMs;
  for (;;) {
    const left = deadline - performance.now();
    if (left <= 0 || dec.ms >= timeoutMs) break;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const chunk = await Promise.race([
      input.read(CHUNK),
      new Promise<undefined>((r) => {
        timer = setTimeout(() => {
          r(undefined);
        }, left);
      }),
    ]);
    clearTimeout(timer);
    if (!chunk?.length) break;
    const rep = dec.push(chunk);
    if (rep) return rep;
  }
  return dec.finish();
}
