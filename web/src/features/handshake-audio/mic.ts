// The microphone as an AudioInput: raw samples (no echo cancellation, noise
// suppression or gain control, which would eat the ultrasonic layer), held in
// memory only until the decoder reads them. Nothing is recorded, uploaded or
// stored (ARCHITECTURE §7). Owner: P1-W13.
import type { AudioInput } from './index';

export interface MicInput extends AudioInput {
  sampleRate: number;
  close(): void;
}

export async function openMic(ctx: AudioContext): Promise<MicInput> {
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: {
      echoCancellation: false,
      noiseSuppression: false,
      autoGainControl: false,
      channelCount: 1,
    },
    video: false,
  });
  if (ctx.state === 'suspended') await ctx.resume();
  const source = ctx.createMediaStreamSource(stream);
  // ScriptProcessorNode is deprecated in favour of AudioWorklet, but an
  // AudioWorklet module would need its own script URL under the CSP; every
  // browser still ships ScriptProcessorNode, and capture is all it does here.
  // eslint-disable-next-line @typescript-eslint/no-deprecated
  const tap = ctx.createScriptProcessor(4096, 1, 1);
  const mute = ctx.createGain();
  mute.gain.value = 0;
  const queue: Float32Array[] = [];
  let wake: (() => void) | undefined;
  // eslint-disable-next-line @typescript-eslint/no-deprecated
  tap.onaudioprocess = (e: AudioProcessingEvent) => {
    // eslint-disable-next-line @typescript-eslint/no-deprecated
    queue.push(e.inputBuffer.getChannelData(0).slice());
    if (queue.length > 64) queue.shift(); // never more than ~5 s behind
    wake?.();
  };
  source.connect(tap);
  tap.connect(mute); // a ScriptProcessorNode only runs when connected to the output
  mute.connect(ctx.destination);
  let closed = false;
  return {
    sampleRate: ctx.sampleRate,
    read: async () => {
      while (!queue.length && !closed) {
        await new Promise<void>((r) => (wake = r));
        wake = undefined;
      }
      return queue.shift() ?? new Float32Array(0);
    },
    close: () => {
      closed = true;
      wake?.();
      // eslint-disable-next-line @typescript-eslint/no-deprecated
      tap.onaudioprocess = null;
      source.disconnect();
      tap.disconnect();
      mute.disconnect();
      for (const t of stream.getTracks()) t.stop();
      queue.length = 0;
    },
  };
}
