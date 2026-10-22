// The posts picked for one reply (§10.1, up to 3 parents): picked from any
// post's links in the feed or the reply tree, shown in the reply tray, and
// handed to the composer as ?parent=…&parent=…. Kept for the tab's session so
// the picks survive moving between pages. Owner: P0-W05 → P1-W12.
import { useSyncExternalStore } from 'react';
import { MAX_PARENTS } from '../post/vm';

export interface Pick {
  ref: string;
  handle: string;
}

const KEY = 'pcc.replyPicks';
const listeners = new Set<() => void>();
let picks: Pick[] = load();

function load(): Pick[] {
  try {
    const raw: unknown = JSON.parse(sessionStorage.getItem(KEY) ?? '[]');
    if (!Array.isArray(raw)) return [];
    return raw
      .filter((p): p is Pick => typeof (p as Pick | null)?.ref === 'string' && typeof (p as Pick).handle === 'string')
      .slice(0, MAX_PARENTS);
  } catch {
    return [];
  }
}

function set(next: Pick[]): void {
  picks = next;
  try {
    sessionStorage.setItem(KEY, JSON.stringify(picks));
  } catch {
    // Storage may be off; the picks still last until the page reloads.
  }
  listeners.forEach((fn) => {
    fn();
  });
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function usePicks(): Pick[] {
  return useSyncExternalStore(subscribe, () => picks);
}

export function getPicks(): Pick[] {
  return picks;
}

// togglePick adds a post to the reply (while fewer than MAX_PARENTS are
// picked) or takes it out.
export function togglePick(p: Pick): void {
  if (picks.some((x) => x.ref === p.ref)) set(picks.filter((x) => x.ref !== p.ref));
  else if (picks.length < MAX_PARENTS) set([...picks, p]);
}

export function clearPicks(): void {
  set([]);
}

// composeURL is the composer's address for a reply to refs (the first
// MAX_PARENTS distinct ones).
export function composeURL(refs: string[]): string {
  const q = new URLSearchParams();
  for (const r of [...new Set(refs)].slice(0, MAX_PARENTS)) q.append('parent', r);
  q.set('kind', 'ref');
  return `/compose?${q.toString()}`;
}

export const REF = /^[A-Za-z0-9_-]+$/;

// refFromLink takes a post's ref from a pasted link to it (…/p/<ref>) or the
// bare ref.
export function refFromLink(text: string): string | undefined {
  const s = text.trim();
  const m = /\/p\/([A-Za-z0-9_-]+)\/?(?:[?#].*)?$/.exec(s);
  const ref = m ? m[1] : s;
  return ref !== undefined && REF.test(ref) ? ref : undefined;
}
