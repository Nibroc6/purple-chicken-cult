// MediaPicker (features/media contract). Owner: P1-W04.
// Choose up to `max` pictures, describe each (alt text), upload them. Every
// file is scanned on the server before anything is stored; a refused one shows
// its error and is not passed on. The picker hides itself once the server says
// media is turned off (feature_disabled).
import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError } from '../../api/errors';
import type { MediaVM } from '../../vm/types';
import type { MediaPickerProps } from './index';
import { useUpload } from './useUpload';
import { ACCEPT, MAX_ALT } from './vm';
import './media.css';

interface Item {
  id: number;
  file: File;
  alt: string;
  preview: string;
  error?: ErrCode;
}

const CODES = ['media_too_large', 'media_type', 'media_rejected', 'alt_too_long', 'rate_limited'] as const;
type ErrCode = (typeof CODES)[number] | 'other';

let nextID = 1;

// errorCode maps an upload failure to its catalog key.
// i18n-dynamic: media:errors.
function errorCode(err: unknown): ErrCode {
  if (err instanceof ApiError) return CODES.find((c) => c === err.code) ?? 'other';
  return 'other';
}

export function MediaPicker(props: MediaPickerProps) {
  const { max } = props;
  const { t } = useTranslation('media');
  const up = useUpload();
  const { pending, disabled } = up;
  const [items, setItems] = useState<Item[]>([]);
  const input = useRef<HTMLInputElement>(null);
  const baseID = useId();
  const limit = Math.max(0, Math.min(max, 4));

  useEffect(
    () => () => {
      items.forEach((i) => {
        URL.revokeObjectURL(i.preview);
      });
    },
    // revoke on unmount only; removed items are revoked when removed
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  if (disabled || limit === 0) return null;

  const choose = (files: FileList | null) => {
    if (!files) return;
    const room = limit - items.length;
    const add = Array.from(files)
      .slice(0, Math.max(0, room))
      .map((file) => ({ id: nextID++, file, alt: '', preview: URL.createObjectURL(file) }));
    setItems((cur) => [...cur, ...add]);
    if (input.current) input.current.value = '';
  };

  const remove = (id: number) => {
    setItems((cur) =>
      cur.filter((i) => {
        if (i.id === id) URL.revokeObjectURL(i.preview);
        return i.id !== id;
      }),
    );
  };

  const send = async () => {
    const done: MediaVM[] = [];
    const left: Item[] = [];
    for (const it of items) {
      try {
        done.push(await up.upload(it.file, it.alt.trim()));
        URL.revokeObjectURL(it.preview);
      } catch (err) {
        left.push({ ...it, error: errorCode(err) });
      }
    }
    setItems(left);
    if (done.length > 0) props.onPicked(done);
  };

  return (
    <div className="media-picker" data-testid="media-picker">
      <label className="media-choose">
        <span>{t('picker.choose', { max: limit })}</span>
        <input
          ref={input}
          type="file"
          accept={ACCEPT}
          multiple={limit > 1}
          disabled={pending || items.length >= limit}
          data-testid="media-file"
          onChange={(e) => {
            choose(e.currentTarget.files);
          }}
        />
      </label>
      {items.length > 0 ? (
        <ul className="media-items">
          {items.map((it) => (
            <li key={it.id} className="media-item" data-testid="media-item">
              <img className="media-preview" src={it.preview} alt="" />
              <div className="media-item-body">
                <label htmlFor={`${baseID}-${String(it.id)}`}>{t('picker.alt')}</label>
                <textarea
                  id={`${baseID}-${String(it.id)}`}
                  data-testid="media-alt"
                  maxLength={MAX_ALT}
                  rows={2}
                  value={it.alt}
                  placeholder={t('picker.altPlaceholder')}
                  onChange={(e) => {
                    const alt = e.currentTarget.value;
                    setItems((cur) => cur.map((c) => (c.id === it.id ? { ...c, alt } : c)));
                  }}
                />
                {it.error ? (
                  <p role="alert" className="media-error" data-testid="media-error" data-code={it.error}>
                    {t(`errors.${it.error}`)}
                  </p>
                ) : null}
                <button
                  type="button"
                  data-testid="media-remove"
                  disabled={pending}
                  onClick={() => {
                    remove(it.id);
                  }}
                >
                  {t('picker.remove')}
                </button>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
      {items.length > 0 ? (
        <button type="button" className="primary" data-testid="media-upload" disabled={pending} onClick={() => void send()}>
          {pending ? t('picker.uploading') : t('picker.upload', { count: items.length })}
        </button>
      ) : null}
      <p className="media-note">{t('picker.note')}</p>
    </div>
  );
}
