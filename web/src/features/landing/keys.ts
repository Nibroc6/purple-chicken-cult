// The published server signing keys (/.well-known/pcc-keys.json, served by
// staticapi; public material, no session needed). Owner: P0-W12.
export interface PublishedKey {
  keyID: string;
  alg: string;
  fingerprint: string;
  validFrom: Date;
  validUntil?: Date;
}

interface RawKey {
  key_id?: unknown;
  alg?: unknown;
  fingerprint?: unknown;
  valid_from?: unknown;
  valid_until?: unknown;
}

export const keysPath = '/.well-known/pcc-keys.json';

export function toKeys(body: unknown): PublishedKey[] {
  const server = (body as { server?: unknown } | null)?.server;
  if (!Array.isArray(server)) return [];
  const out: PublishedKey[] = [];
  for (const k of server as RawKey[]) {
    if (typeof k.key_id !== 'string' || typeof k.fingerprint !== 'string' || typeof k.valid_from !== 'string') continue;
    if (!/^[0-9a-f]{64}$/.test(k.fingerprint)) continue;
    out.push({
      keyID: k.key_id,
      alg: typeof k.alg === 'string' ? k.alg : '',
      fingerprint: k.fingerprint,
      validFrom: new Date(k.valid_from),
      validUntil: typeof k.valid_until === 'string' ? new Date(k.valid_until) : undefined,
    });
  }
  return out;
}

export async function fetchKeys(): Promise<PublishedKey[]> {
  const res = await globalThis.fetch(new Request(`${globalThis.location.origin}${keysPath}`, { credentials: 'omit' }));
  if (!res.ok) throw new Error('keys');
  return toKeys(await res.json());
}

// groupFingerprint splits a hex fingerprint into groups of four for reading aloud.
export function groupFingerprint(fp: string): string {
  return (fp.match(/.{1,4}/g) ?? []).join(' ');
}
