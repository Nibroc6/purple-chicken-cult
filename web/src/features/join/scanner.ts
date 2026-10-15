// The invitee's QR scanner: the rear camera through getUserMedia, frames drawn
// to an offscreen canvas a few times a second and decoded in a module worker
// created through the Trusted Types script-URL policy (plan §6.6). Nothing is
// recorded or uploaded; only a decoded invite frame leaves the device.
// Owner: P0-W04 → P1-W02.
import workerURL from './scan.worker.ts?worker&url';
import { scriptURL } from '../../lib/trustedTypes';

const EVERY_MS = 250;
const MAX_SIDE = 640;

export interface Scanner {
  stop: () => void;
}

export class CameraError extends Error {
  constructor() {
    super('camera');
    this.name = 'CameraError';
  }
}

// startScanner shows the camera in video and calls onFrame with every decoded
// invite frame until stop() is called.
export async function startScanner(video: HTMLVideoElement, onFrame: (payload: string) => void): Promise<Scanner> {
  let stream: MediaStream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' }, audio: false });
  } catch {
    throw new CameraError();
  }
  video.srcObject = stream;
  video.muted = true;
  video.playsInline = true;
  await video.play().catch(() => undefined);

  const worker = new Worker(scriptURL(workerURL), { type: 'module' });
  const canvas = document.createElement('canvas');
  const ctx = canvas.getContext('2d', { willReadFrequently: true });
  let busy = false;
  let stopped = false;
  worker.addEventListener('message', (e: MessageEvent<string | null>) => {
    busy = false;
    if (!stopped && e.data) onFrame(e.data);
  });
  const timer = setInterval(() => {
    if (busy || stopped || !ctx || video.videoWidth === 0) return;
    const scale = Math.min(1, MAX_SIDE / Math.max(video.videoWidth, video.videoHeight));
    canvas.width = Math.round(video.videoWidth * scale);
    canvas.height = Math.round(video.videoHeight * scale);
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
    const img = ctx.getImageData(0, 0, canvas.width, canvas.height);
    busy = true;
    worker.postMessage({ data: img.data, width: img.width, height: img.height }, [img.data.buffer]);
  }, EVERY_MS);

  return {
    stop: () => {
      stopped = true;
      clearInterval(timer);
      worker.terminate();
      stream.getTracks().forEach((t) => {
        t.stop();
      });
      video.srcObject = null;
    },
  };
}
