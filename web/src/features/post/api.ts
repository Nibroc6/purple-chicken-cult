// Posts API calls (plan §5.3 postsapi). Owner: P0-W05 → P1-W12.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import type { Audience, PostVM, RefPolicy } from '../../vm/types';
import { toPostVM } from './vm';

type S = components['schemas'];

export interface ParentIn {
  ref: string;
  kind: 'ref' | 'cont';
}

export interface DraftIn {
  body: string;
  lang: string;
  audience: Audience;
  refPolicy: RefPolicy;
  noPreview: boolean;
  parents: ParentIn[];
}

// postKey prefixes every post query (['post', ref]).
export const postKey = ['post'] as const;

const at = (ref: string) => ({ params: { path: { ref } } });

function ok(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

function shared(d: DraftIn): Pick<S['PostsDraft'], 'audience' | 'ref_policy' | 'no_preview' | 'parents'> {
  return {
    audience: d.audience,
    ref_policy: d.refPolicy,
    ...(d.noPreview ? { no_preview: true } : {}),
    ...(d.parents.length > 0 ? { parents: d.parents } : {}),
  };
}

export async function fetchPost(ref: string): Promise<PostVM> {
  return toPostVM(unwrap(await api.GET('/posts/{ref}', at(ref))));
}

export async function createPost(d: DraftIn): Promise<PostVM> {
  return toPostVM(unwrap(await api.POST('/posts', { body: { body: d.body, lang: d.lang, ...shared(d) } })));
}

// createThread publishes parts as one continuation thread (one write).
export async function createThread(d: DraftIn, parts: string[]): Promise<PostVM[]> {
  const out = unwrap(
    await api.POST('/posts/thread', { body: { parts: parts.map((body) => ({ body, lang: d.lang })), ...shared(d) } }),
  );
  return out.items.map(toPostVM);
}

export async function editPost(ref: string, body: string, lang: string): Promise<PostVM> {
  return toPostVM(unwrap(await api.PATCH('/posts/{ref}', { ...at(ref), body: { body, lang } })));
}

export async function deletePost(ref: string): Promise<void> {
  ok(await api.DELETE('/posts/{ref}', at(ref)));
}

// setRefPolicy returns whether to offer "detach all" (the post turned slippery
// while references to it are attached).
export async function setRefPolicy(ref: string, policy: RefPolicy): Promise<boolean> {
  return unwrap(await api.PUT('/posts/{ref}/ref-policy', { ...at(ref), body: { ref_policy: policy } })).offer_detach_all;
}

export async function detachChild(ref: string, child: string): Promise<void> {
  ok(await api.POST('/posts/{ref}/detach', { ...at(ref), body: { child } }));
}

export async function detachAll(ref: string): Promise<void> {
  ok(await api.POST('/posts/{ref}/detach-all', at(ref)));
}
