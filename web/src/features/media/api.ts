// Media API calls (plan §5.3 mediaapi). Owner: P1-W04.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import type { MediaVM } from '../../vm/types';
import { toMediaVM } from './vm';

export type MediaViewJSON = components['schemas']['MediaView'];
export type Variant = 'thumb' | 'display' | 'original';

// uploadMedia sends one file as multipart (the server scans it before storing
// anything) and returns the stored image's view-model.
export async function uploadMedia(file: Blob, alt: string): Promise<MediaVM> {
  const form = new FormData();
  form.append('file', file);
  form.append('alt', alt);
  const res = await api.POST('/media', {
    body: form as unknown as components['schemas']['MediaUploadIn'],
    bodySerializer: (b: unknown) => b as FormData,
  });
  if (res.response.status !== 201 || !res.data) throw new ApiError(res.response.status, res.error);
  return toMediaVM(res.data);
}

export interface SignedURL {
  url: string;
  expiresAt: Date;
}

// mediaURL asks the gate for a signed per-viewer URL of one variant.
export async function mediaURL(ref: string, variant: Variant): Promise<SignedURL> {
  const out = unwrap(await api.GET('/media/{ref}/url', { params: { path: { ref }, query: { variant } } }));
  return { url: out.url, expiresAt: new Date(out.expires_at) };
}
