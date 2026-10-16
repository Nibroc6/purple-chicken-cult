// useUpload (features/media contract). Owner: P1-W04.
// Scaffold placeholder: uploads are disabled (fail closed).
import type { UploadState } from './index';

export function useUpload(): UploadState {
  return {
    upload: () => Promise.reject(new Error('feature_disabled')),
    pending: false,
    disabled: true,
  };
}
