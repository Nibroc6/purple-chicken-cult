// Picture check: upload pictures through the same picker the composer uses and
// see them back through signed URLs (thumb, display, and the original on
// request). It stands in until posts carry media (P1-W12). Owner: P1-W04.
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { MediaVM } from '../../vm/types';
import { MediaImage } from './MediaImage';
import { MediaPicker } from './MediaPicker';
import { useMediaDisabled } from './state';
import './media.css';

export default function CheckPage() {
  const { t } = useTranslation('media');
  const disabled = useMediaDisabled();
  const [media, setMedia] = useState<MediaVM[]>([]);
  const [open, setOpen] = useState<{ ref: string; original: boolean }>();
  return (
    <main className="media-check">
      <h1>{t('check.title')}</h1>
      <p>{t('check.body')}</p>
      {disabled ? (
        <p role="status" data-testid="media-disabled">
          {t('check.disabled')}
        </p>
      ) : (
        <MediaPicker
          max={4}
          onPicked={(m) => {
            setMedia((cur) => [...cur, ...m]);
          }}
        />
      )}
      {media.length > 0 ? (
        <ul className="media-grid" data-testid="media-uploaded">
          {media.map((m) => {
            const shown = open?.ref === m.ref;
            return (
              <li key={m.ref}>
                <button
                  type="button"
                  className="media-thumb-button"
                  data-testid="media-open"
                  aria-expanded={shown}
                  onClick={() => {
                    setOpen(shown ? undefined : { ref: m.ref, original: false });
                  }}
                >
                  <MediaImage media={m} variant="thumb" />
                </button>
                {shown ? (
                  <div className="media-large">
                    <MediaImage media={m} variant={open.original ? 'original' : 'display'} />
                    {!open.original ? (
                      <button
                        type="button"
                        data-testid="media-original"
                        onClick={() => {
                          setOpen({ ref: m.ref, original: true });
                        }}
                      >
                        {t('check.original')}
                      </button>
                    ) : null}
                  </div>
                ) : null}
              </li>
            );
          })}
        </ul>
      ) : null}
    </main>
  );
}
