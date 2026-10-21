// Saves API calls (plan §5.3 engageapi). Owner: P1-W08.
import { api, unwrap } from '../../api/client';
import type { PostVM } from '../../vm/types';
import { toPostVM } from '../post/vm';

export const savesKey = ['saves'] as const;

export interface SavesPageVM {
  items: PostVM[];
  next?: string;
}

export async function fetchSaves(cursor: string | undefined): Promise<SavesPageVM> {
  const page = unwrap(await api.GET('/saves', { params: { query: cursor ? { cursor } : {} } }));
  return { items: page.items.map(toPostVM), ...(page.next ? { next: page.next } : {}) };
}
