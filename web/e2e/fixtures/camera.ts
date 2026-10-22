// A second Chromium whose fake camera shows a given QR code (P0-W04 → P1-W02):
// the invitee's browser in the invite e2e specs. The camera file is a Y4M rendered
// by scripts/gen-y4m.mjs; camera permission is granted by the fake UI flag.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { chromium, type Browser, type Page } from '@playwright/test';
import { writeY4M } from '../../scripts/gen-y4m.mjs';

export interface CameraBrowser {
  browser: Browser;
  close(): Promise<void>;
}

// launchWithCamera starts Chromium with a fake camera that films payload as a QR code.
export async function launchWithCamera(payload: string): Promise<CameraBrowser> {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pcc-cam-'));
  const file = path.join(dir, 'qr.y4m');
  writeY4M(payload, file);
  const browser = await chromium.launch({
    args: [
      '--use-fake-ui-for-media-stream',
      '--use-fake-device-for-media-stream',
      `--use-file-for-fake-video-capture=${file}`,
    ],
  });
  return {
    browser,
    close: async () => {
      await browser.close();
      fs.rmSync(dir, { recursive: true, force: true });
    },
  };
}

// ---- the handshake sound through the AudioInput seam (P1-W02) ----
//
// The sound codec (features/handshake-audio) is replaced by a loopback over
// the seam of features/invites/audio.ts: the claiming device's play() records
// the bits it would play; the test hands them (or some of them) to the
// initiator's page, whose AudioInput yields them as PCM samples to listen().
// Nothing else of the flow is faked: frames, keys, kmac, the server's checks
// and the passkey ceremonies are the real ones.

export interface PlayedBits {
  ultra: number[];
  jingle: number[];
}

declare global {
  interface Window {
    __pccPlayed?: PlayedBits[];
    __pccHeard?: (PlayedBits | null)[];
  }
}

// installPlayer makes page's handshake sound a recorder (the claiming device).
export async function installPlayer(page: Page): Promise<void> {
  await page.addInitScript(() => {
    window.__pccPlayed = [];
    (globalThis as Record<string, unknown>).pccHandshakeAudio = () => ({
      play: (bits: { ultra: Uint8Array; jingle: Uint8Array }) => {
        window.__pccPlayed?.push({ ultra: Array.from(bits.ultra), jingle: Array.from(bits.jingle) });
        return Promise.resolve({ startedAt: performance.now() });
      },
      listen: () => Promise.reject(new Error('player')),
    });
  });
}

// installListener makes page's handshake sound decode whatever the test
// relays through the AudioInput seam (the initiator's device). The PCM layout
// is [hasUltra, u0..u3, hasJingle, j0..j2], each byte as a sample / 255.
export async function installListener(page: Page): Promise<void> {
  await page.addInitScript(() => {
    window.__pccHeard = [];
    const input = {
      read: async (frames: number) => {
        for (;;) {
          const h = window.__pccHeard?.shift();
          if (h !== undefined) {
            const out = new Float32Array(frames);
            if (h) {
              out[0] = h.ultra.length ? 1 : 0;
              h.ultra.forEach((b, i) => (out[1 + i] = b / 255));
              out[5] = h.jingle.length ? 1 : 0;
              h.jingle.forEach((b, i) => (out[6 + i] = b / 255));
            }
            return out;
          }
          await new Promise((r) => setTimeout(r, 50));
        }
      },
    };
    (globalThis as Record<string, unknown>).pccAudioInput = input;
    const bytes = (pcm: Float32Array, from: number, n: number) =>
      Uint8Array.from(Array.from(pcm.slice(from, from + n), (v) => Math.round(v * 255)));
    (globalThis as Record<string, unknown>).pccHandshakeAudio = () => ({
      play: () => Promise.reject(new Error('listener')),
      listen: async (opts: { input?: { read(frames: number): Promise<Float32Array> } }) => {
        const pcm = await (opts.input ?? input).read(9);
        return {
          ...(pcm[0] ? { ultraBits: bytes(pcm, 1, 4), tUltraMs: 700 } : {}),
          ...(pcm[5] ? { jingleBits: bytes(pcm, 6, 3), tJingleMs: 900 } : {}),
          ...(pcm[0] && pcm[5] ? { offsetMs: 10 } : {}),
          motifLevelDb: pcm[5] ? -20 : -80,
        };
      },
    });
  });
}

// relaySound waits for the next sound the player played and lets the listener
// hear the layers asked for (none: the listener heard nothing).
export async function relaySound(
  player: Page,
  listener: Page,
  hear: { ultra: boolean; jingle: boolean },
): Promise<PlayedBits> {
  const handle = await player.waitForFunction(() => window.__pccPlayed?.shift(), undefined, { timeout: 20_000 });
  const bits = (await handle.jsonValue()) as PlayedBits;
  const heard = hear.ultra || hear.jingle ? { ultra: hear.ultra ? bits.ultra : [], jingle: hear.jingle ? bits.jingle : [] } : null;
  await listener.evaluate((h) => window.__pccHeard?.push(h), heard);
  return bits;
}
