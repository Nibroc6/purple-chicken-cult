// The unsent draft of a reply, kept for the tab (sessionStorage) so
// leaving to read a post it replies to and coming back loses nothing. Keyed by
// the page's query (its parents); cleared on publish. Storage can be missing
// or full: then nothing is kept.
import type { ParentIn } from '../post/api';
import type { Audience, RefPolicy } from '../../vm/types';

export interface SavedDraft {
  parents: ParentIn[];
  body: string;
  parts: string[] | null;
  lang: string | null;
  audience: Audience;
  policy: RefPolicy;
  noPreview: boolean;
}

const key = (query: string) => `pcc:compose:${query}`;

export function loadDraft(query: string): Partial<SavedDraft> {
  try {
    const raw = sessionStorage.getItem(key(query));
    return raw ? (JSON.parse(raw) as Partial<SavedDraft>) : {};
  } catch {
    return {};
  }
}

export function saveDraft(query: string, d: SavedDraft): void {
  try {
    if (d.body === '' && d.parts === null) sessionStorage.removeItem(key(query));
    else sessionStorage.setItem(key(query), JSON.stringify(d));
  } catch {
    // Not kept.
  }
}

export function clearDraft(query: string): void {
  try {
    sessionStorage.removeItem(key(query));
  } catch {
    // Nothing to clear.
  }
}
