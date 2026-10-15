// Module worker that decodes camera frames off the main thread (plan §2.2:
// jsqr in a module Worker). Message in: {data, width, height}; out: string | null.
// Owner: P0-W04 → P1-W02.
import { decodeFrame } from './decode';

interface FrameMsg {
  data: Uint8ClampedArray;
  width: number;
  height: number;
}

globalThis.addEventListener('message', (e: MessageEvent<FrameMsg>) => {
  const { data, width, height } = e.data;
  globalThis.postMessage(decodeFrame(data, width, height));
});
