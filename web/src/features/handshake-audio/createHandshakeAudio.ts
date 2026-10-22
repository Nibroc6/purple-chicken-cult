// createHandshakeAudio (features/handshake-audio contract). Owner: P1-W13.
// play renders the sound (synth.ts) into an AudioBuffer and plays it once;
// listen decodes the microphone, or opts.input when given (the AudioInput test
// seam), until a sound is decoded, the input ends or timeoutMs passes.
import type { AudioInput, DecodeReport, HandshakeAudio, PlayBits } from './index';
import { StreamDecoder } from './decoder';
import { openMic } from './mic';
import { checkBits, renderSound } from './synth';

const CHUNK = 4096;
// How long play waits for a suspended context to start (Safari leaves resume()
// pending forever outside a user gesture) and, past the sound's length, for it
// to end.
const RESUME_MS = 2000;
const END_SLACK_MS = 3000;

interface AudioSessionNav {
  audioSession?: { type: string };
}

// playbackSession asks iOS Safari (17+) to treat the sound as media playback,
// which plays even with the ring/silent switch on; it returns the undo.
function playbackSession(): () => void {
  const s = (navigator as AudioSessionNav).audioSession;
  if (!s || s.type !== 'auto') return () => undefined;
  try {
    s.type = 'playback';
  } catch {
    return () => undefined;
  }
  return () => {
    s.type = 'auto';
  };
}

// started resumes ctx if needed, and throws audio_blocked if it won't run.
async function started(ctx: AudioContext): Promise<void> {
  if (ctx.state !== 'running') {
    let timer: ReturnType<typeof setTimeout> | undefined;
    await Promise.race([
      ctx.resume().catch(() => undefined),
      new Promise<void>((r) => {
        timer = setTimeout(r, RESUME_MS);
      }),
    ]);
    clearTimeout(timer);
  }
  if (ctx.state !== 'running') throw new Error('audio_blocked');
}

export function createHandshakeAudio(ctx: AudioContext): HandshakeAudio {
  return {
    play: async (bits: PlayBits, opts: { quiet: boolean }) => {
      checkBits(bits);
      const restore = playbackSession();
      try {
        await started(ctx);
      } catch (e) {
        restore();
        throw e;
      }
      const pcm = renderSound(bits, {
        quiet: opts.quiet,
        sampleRate: ctx.sampleRate,
      });
      const buf = ctx.createBuffer(1, pcm.length, ctx.sampleRate);
      buf.copyToChannel(pcm, 0);
      const src = ctx.createBufferSource();
      src.buffer = buf;
      src.connect(ctx.destination);
      return new Promise<{ startedAt: number }>((resolve, reject) => {
        const startedAt = performance.now();
        const done = (err?: Error) => {
          clearTimeout(timer);
          src.onended = null;
          src.disconnect();
          restore();
          if (err) reject(err);
          else resolve({ startedAt });
        };
        // A context interrupted mid-sound never fires onended.
        const timer = setTimeout(() => {
          done(new Error('audio_stalled'));
        }, (pcm.length / ctx.sampleRate) * 1000 + END_SLACK_MS);
        src.onended = () => {
          done();
        };
        try {
          src.start();
        } catch (e) {
          done(e instanceof Error ? e : new Error('audio_blocked'));
        }
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
