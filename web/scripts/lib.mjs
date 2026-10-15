// Shared helpers for the generator scripts: an exclusive lock (web/.gen.lock) and
// atomic writes (temp file + rename), so concurrent runs never expose a partial file.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const repoDir = path.resolve(webDir, '..');

const lockPath = path.join(webDir, '.gen.lock');

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function withLock(fn) {
  const deadline = Date.now() + 60_000;
  for (;;) {
    try {
      fs.mkdirSync(lockPath);
      break;
    } catch (err) {
      if (err.code !== 'EEXIST') throw err;
      try {
        const age = Date.now() - fs.statSync(lockPath).mtimeMs;
        if (age > 120_000) fs.rmSync(lockPath, { recursive: true, force: true });
      } catch {
        // the other process just released it
      }
      if (Date.now() > deadline) throw new Error('timed out waiting for web/.gen.lock', { cause: err });
      await sleep(100);
    }
  }
  try {
    return await fn();
  } finally {
    fs.rmSync(lockPath, { recursive: true, force: true });
  }
}

export function writeAtomic(file, contents) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  if (fs.existsSync(file) && fs.readFileSync(file, 'utf8') === contents) return;
  const tmp = `${file}.${process.pid}.${Date.now()}.tmp`;
  fs.writeFileSync(tmp, contents);
  fs.renameSync(tmp, file);
}

// flatten turns nested catalog JSON into { "a.b": "message" }.
export function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === 'object') flatten(v, key, out);
    else out[key] = String(v);
  }
  return out;
}

export function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

export const localesDir = path.join(webDir, 'src', 'i18n', 'locales');
