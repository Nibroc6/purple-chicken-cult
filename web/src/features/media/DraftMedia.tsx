// Draft pictures for the composer. Owner: P1-W04.
// A picture uploads as soon as it is picked (one at a time: writes of one kind
// are spaced), so Publish only attaches it. Alt text can be written meanwhile
// and is sent at publish (PATCH /media/{ref}). Removing a picture, or leaving
// the composer without publishing, discards its upload (DELETE /media/{ref});
// the server also deletes uploads left unattached after two hours. Every file
// is still scanned on the server before anything is stored.
import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { MediaVM, Ref } from '../../vm/types';
import { discardMedia, setMediaAlt } from './api';
import { errorCode, type ErrCode } from './errors';
import { useUpload } from './useUpload';
import { ACCEPT, MAX_ALT } from './vm';
import './media.css';

export interface DraftItem {
  id: number;
  file: File;
  preview: string;
  alt: string;
  status: 'uploading' | 'ready' | 'error';
  media?: MediaVM;
  error?: ErrCode;
}

export interface DraftMedia {
  items: DraftItem[];
  uploading: boolean; // some picture is still uploading
  failed: boolean; // some picture was refused (remove it to publish)
  disabled: boolean; // the server reported media as turned off
  add(files: File[]): void;
  remove(id: number): void;
  setAlt(id: number, alt: string): void;
  // finalize waits for the uploads, sends changed alt text and returns the
  // refs in order. It throws if a picture failed.
  finalize(): Promise<Ref[]>;
  // commit keeps the uploads: they now belong to a published post.
  commit(): void;
}

let nextID = 1;

export function useDraftMedia(): DraftMedia {
  const up = useUpload();
  const [items, setItems] = useState<DraftItem[]>([]);
  const cur = useRef<DraftItem[]>([]);
  const queue = useRef<Promise<void>>(Promise.resolve());
  const done = useRef(false); // committed or unmounted: nothing more to keep

  const update = useCallback((fn: (items: DraftItem[]) => DraftItem[]) => {
    cur.current = fn(cur.current);
    setItems(cur.current);
  }, []);
  const patch = useCallback(
    (id: number, p: Partial<DraftItem>) => {
      update((xs) => xs.map((x) => (x.id === id ? { ...x, ...p } : x)));
    },
    [update],
  );

  const run = useCallback(
    async (it: DraftItem) => {
      if (!cur.current.some((x) => x.id === it.id)) return; // removed before its turn
      try {
        const media = await up.upload(it.file, '');
        if (done.current || !cur.current.some((x) => x.id === it.id)) {
          void discardMedia(media.ref).catch(() => undefined); // removed while uploading
          return;
        }
        patch(it.id, { status: 'ready', media });
      } catch (err) {
        patch(it.id, { status: 'error', error: errorCode(err) });
      }
    },
    [up, patch],
  );

  const add = useCallback(
    (files: File[]) => {
      const added = files.map(
        (file): DraftItem => ({
          id: nextID++,
          file,
          preview: URL.createObjectURL(file),
          alt: '',
          status: 'uploading',
        }),
      );
      update((xs) => [...xs, ...added]);
      for (const it of added) queue.current = queue.current.then(() => run(it));
    },
    [update, run],
  );

  const remove = useCallback(
    (id: number) => {
      const it = cur.current.find((x) => x.id === id);
      if (!it) return;
      URL.revokeObjectURL(it.preview);
      if (it.media) void discardMedia(it.media.ref).catch(() => undefined);
      update((xs) => xs.filter((x) => x.id !== id));
    },
    [update],
  );

  const setAlt = useCallback(
    (id: number, alt: string) => {
      patch(id, { alt });
    },
    [patch],
  );

  const finalize = useCallback(async (): Promise<Ref[]> => {
    while (cur.current.some((x) => x.status === 'uploading')) await queue.current;
    if (cur.current.some((x) => x.status === 'error')) throw new Error('media_failed');
    const refs: Ref[] = [];
    for (const it of cur.current) {
      if (!it.media) continue;
      const alt = it.alt.trim();
      if (alt !== (it.media.alt ?? '')) patch(it.id, { media: await setMediaAlt(it.media.ref, alt) });
      refs.push(it.media.ref);
    }
    return refs;
  }, [patch]);

  const commit = useCallback(() => {
    done.current = true;
  }, []);

  // Leaving without publishing discards the uploads; so does closing the tab
  // (a page kept in the back/forward cache keeps them).
  useEffect(() => {
    done.current = false; // a remount (StrictMode) starts over
    const leave = (keepalive: boolean) => {
      for (const it of cur.current) if (it.media) void discardMedia(it.media.ref, keepalive).catch(() => undefined);
    };
    const onHide = (e: PageTransitionEvent) => {
      if (!e.persisted && !done.current) leave(true);
    };
    window.addEventListener('pagehide', onHide);
    return () => {
      window.removeEventListener('pagehide', onHide);
      if (!done.current) leave(false);
      done.current = true;
      cur.current.forEach((x) => {
        URL.revokeObjectURL(x.preview);
      });
    };
  }, []);

  return {
    items,
    uploading: items.some((x) => x.status === 'uploading'),
    failed: items.some((x) => x.status === 'error'),
    disabled: up.disabled,
    add,
    remove,
    setAlt,
    finalize,
    commit,
  };
}

// DraftMediaPicker shows the draft's pictures (preview, upload status, alt
// text, remove) and the file input for the rest of max. It hides itself once
// the server says media is turned off.
// i18n-dynamic: media:errors.
export function DraftMediaPicker({ draft, max }: { draft: DraftMedia; max: number }) {
  const { t } = useTranslation('media');
  const input = useRef<HTMLInputElement>(null);
  const baseID = useId();
  const limit = Math.max(0, Math.min(max, 4));
  if (draft.disabled || limit === 0) return null;
  const room = limit - draft.items.length;
  return (
    <div className="media-picker" data-testid="media-picker">
      <label className="media-choose">
        <span>{t('picker.choose', { max: limit })}</span>
        <input
          ref={input}
          type="file"
          accept={ACCEPT}
          multiple={limit > 1}
          disabled={room <= 0}
          data-testid="media-file"
          onChange={(e) => {
            const files = Array.from(e.currentTarget.files ?? []).slice(0, Math.max(0, room));
            if (files.length > 0) draft.add(files);
            if (input.current) input.current.value = '';
          }}
        />
      </label>
      {draft.items.length > 0 ? (
        <ul className="media-items">
          {draft.items.map((it) => (
            <li key={it.id} className="media-item" data-testid="media-item" data-status={it.status} aria-busy={it.status === 'uploading'}>
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
                    draft.setAlt(it.id, e.currentTarget.value);
                  }}
                />
                {it.status === 'error' && it.error ? (
                  <p role="alert" className="media-error" data-testid="media-error" data-code={it.error}>
                    {t(`errors.${it.error}`)}
                  </p>
                ) : (
                  <p className="media-status muted" role="status" data-testid="media-status">
                    {it.status === 'uploading' ? t('picker.uploading') : t('picker.uploaded')}
                  </p>
                )}
                <button
                  type="button"
                  data-testid="media-remove"
                  onClick={() => {
                    draft.remove(it.id);
                  }}
                >
                  {t('picker.remove')}
                </button>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
