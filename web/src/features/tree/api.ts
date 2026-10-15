// Tree API calls (plan §5.3 treeapi). Owner: P0-W08 → P1-W09.
import { api, unwrap } from '../../api/client';
import type { Preset, Ref, TreeRowVM, TreeVM } from '../../vm/types';
import { toTreeRowVM, toTreeVM } from './vm';

// openTree opens the tree around focal (preset undefined = the saved one).
export async function openTree(focal: string, preset?: Preset): Promise<TreeVM> {
  const query = preset ? { preset } : {};
  return toTreeVM(unwrap(await api.GET('/tree/{ref}', { params: { path: { ref: focal }, query } })));
}

// fetchRow re-centers / loads row gen of a snapshot: center's replies (gen ≥ 0)
// or parents (gen < 0).
export async function fetchRow(snapshot: string, gen: number, center: Ref): Promise<TreeRowVM> {
  return toTreeRowVM(
    unwrap(await api.GET('/tree/s/{snapshot}/row', { params: { path: { snapshot }, query: { gen, center } } })),
  );
}

// fetchMore loads the next page of a row.
export async function fetchMore(snapshot: string, gen: number, center: Ref, cursor: string): Promise<TreeRowVM> {
  return toTreeRowVM(
    unwrap(await api.GET('/tree/s/{snapshot}/more', { params: { path: { snapshot }, query: { gen, center, cursor } } })),
  );
}
