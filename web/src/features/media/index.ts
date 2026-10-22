// Media contract (SCAFFOLD-1; plan §5 P1). The types below are frozen; the
// implementations live in P1-W04's own files (features/media/** except this
// file), which this module re-exports. Uploads fail closed: when the server says
// feature_disabled, the picker hides itself.
import type { MediaVM, Ref } from '../../vm/types';

export interface MediaPickerProps {
  max: number; // ≤ 4 per post
  onPicked(media: MediaVM[]): void;
}

export interface UploadState {
  upload(file: File, alt: string): Promise<MediaVM>;
  pending: boolean;
  disabled: boolean; // server reported feature_disabled
}

// MediaImage re-requests its signed URL on a 403 (URLs expire after ~1 h).
export interface MediaImageProps {
  media: MediaVM;
  variant: 'thumb' | 'display' | 'original';
}

export { MediaPicker } from './MediaPicker';
export { useUpload } from './useUpload';
export { MediaImage } from './MediaImage';
export { DraftMediaPicker, useDraftMedia, type DraftMedia } from './DraftMedia';

export type { Ref };
