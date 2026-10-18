// Starts one pcc server per e2e run (plan §7, buildability A4): the binary built
// by scripts/e2e.sh (never `go run`), a free port, a fresh database cloned from
// the template (PCC_DATABASE_URL set by scripts/e2e.sh) and a per-run static dir.
import { spawn, type ChildProcess } from 'node:child_process';
import { generateKeyPairSync } from 'node:crypto';
import net from 'node:net';
import path from 'node:path';

export interface RunningServer {
  baseURL: string;
  stop(): Promise<void>;
}

export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(() => {
        resolve(port);
      });
    });
  });
}

async function waitReady(url: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${url}/api/session`, { headers: { 'Sec-Fetch-Site': 'same-origin' } });
      if (res.ok) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`pcc did not start at ${url}`);
}

// vapidKeys is a fresh VAPID (P-256) key pair per run (contract request
// p1-w05-1): push is enabled in e2e runs as in a prod deploy with VAPID keys,
// and specs deliver to their own local fake push endpoints only (loopback
// endpoints are accepted under PCC_ENV=test with test hooks, never otherwise).
function vapidKeys(): { public: string; private: string } {
  const { publicKey, privateKey } = generateKeyPairSync('ec', { namedCurve: 'prime256v1' });
  const pub = publicKey.export({ format: 'jwk' });
  const priv = privateKey.export({ format: 'jwk' });
  const raw = Buffer.concat([Buffer.from([4]), Buffer.from(pub.x ?? '', 'base64url'), Buffer.from(pub.y ?? '', 'base64url')]);
  return { public: raw.toString('base64url'), private: priv.d ?? '' };
}

export async function startServer(): Promise<RunningServer> {
  const repo = path.resolve(import.meta.dirname, '..', '..', '..');
  const bin = process.env.PCC_E2E_BIN ?? path.join(repo, 'var', process.platform === 'win32' ? 'pcc.exe' : 'pcc');
  const port = await freePort();
  const baseURL = `http://localhost:${String(port)}`; // WebAuthn refuses IP origins
  const vapid = vapidKeys();
  const child: ChildProcess = spawn(bin, ['serve'], {
    cwd: repo,
    stdio: ['ignore', 'inherit', 'inherit'],
    env: {
      ...process.env,
      PCC_ENV: 'test',
      PCC_TEST_HOOKS: '1',
      PCC_HTTP_ADDR: `127.0.0.1:${String(port)}`,
      PCC_ORIGIN: baseURL,
      PCC_AUTO_MIGRATE: '1',
      PCC_VAPID_PUBLIC: process.env.PCC_VAPID_PUBLIC ?? vapid.public,
      PCC_VAPID_PRIVATE: process.env.PCC_VAPID_PRIVATE ?? vapid.private,
      PCC_VAPID_SUBJECT: process.env.PCC_VAPID_SUBJECT ?? 'mailto:e2e@pcc.invalid',
    },
  });
  // Probe the loopback address directly: Node may resolve localhost to ::1 first.
  await waitReady(`http://127.0.0.1:${String(port)}`, 30_000);
  return {
    baseURL,
    stop: () =>
      new Promise<void>((resolve) => {
        if (child.exitCode !== null) {
          resolve();
          return;
        }
        child.once('exit', () => {
          resolve();
        });
        child.kill();
      }),
  };
}
