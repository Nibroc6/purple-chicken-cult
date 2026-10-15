// A second Chromium whose fake camera shows a given QR code (P0-W04): the
// invitee's browser in the invite e2e spec. The camera file is a Y4M rendered
// by scripts/gen-y4m.mjs; camera permission is granted by the fake UI flag.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { chromium, type Browser } from '@playwright/test';
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
