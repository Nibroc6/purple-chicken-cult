// Media contract (SCAFFOLD-1; plan §5 P1). Implementations are P1-W04's
// (features/media/** except this file). Uploads fail closed: when the server says
// feature_disabled, the picker hides itself.
import type { ComponentType } from 'react';
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

export interface MediaImageProps {
  media: MediaVM;
  variant: 'thumb' | 'display' | 'original';
}

// Placeholders until P1-W04 lands: no picker, no upload, no image.
export const MediaPicker: ComponentType<MediaPickerProps> = () => null;

export function useUpload(): UploadState {
  return {
    upload: () => Promise.reject(new Error('feature_disabled')),
    pending: false,
    disabled: true,
  };
}

// MediaImage re-requests its signed URL on a 403 (URLs expire after ~1 h).
export const MediaImage: ComponentType<MediaImageProps> = () => null;

export type { Ref };
