// useUpload (features/media contract). Owner: P1-W04.
// Uploads one file with its alt text. A feature_disabled answer marks media
// off for the session, so pickers hide themselves; other failures reject with
// the ApiError (codes media_too_large, media_type, media_rejected, …). A short
// write spacing (rate_limited with retry_after_s ≤ 5) is waited out once.
import { useCallback, useState } from 'react';
import { ApiError } from '../../api/errors';
import type { MediaVM } from '../../vm/types';
import { uploadMedia } from './api';
import type { UploadState } from './index';
import { setMediaDisabled, useMediaDisabled } from './state';
import { MAX_BYTES } from './vm';

export function useUpload(): UploadState {
  const disabled = useMediaDisabled();
  const [pending, setPending] = useState(0);
  const upload = useCallback(async (file: File, alt: string): Promise<MediaVM> => {
    if (file.size > MAX_BYTES) throw new ApiError(422, { error: 'media_too_large', field: 'file' });
    setPending((n) => n + 1);
    try {
      try {
        return await uploadMedia(file, alt);
      } catch (err) {
        // Writes of one kind are spaced (§17.4): wait out a short spacing once.
        const wait = err instanceof ApiError && err.code === 'rate_limited' ? err.retryAfterS : undefined;
        if (wait === undefined || wait > 5) throw err;
        await new Promise((r) => setTimeout(r, wait * 1000 + 100));
        return await uploadMedia(file, alt);
      }
    } catch (err) {
      if (err instanceof ApiError && err.code === 'feature_disabled') setMediaDisabled(true);
      throw err;
    } finally {
      setPending((n) => n - 1);
    }
  }, []);
  return { upload, pending: pending > 0, disabled };
}
