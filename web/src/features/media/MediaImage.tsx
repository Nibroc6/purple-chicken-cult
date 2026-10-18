// MediaImage (features/media contract). Owner: P1-W04.
// Fetches a signed per-viewer URL for the variant and renders it lazily. When
// the image fails to load (the media origin answers 403 once the ~1 h URL has
// expired, or refuses it), it asks for a fresh URL and tries again; a second
// failure in a row shows the alt text in a placeholder.
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { MediaImageProps } from './index';
import { refresh, signedURL } from './urls';
import './media.css';

interface Shown {
  key: string;
  src?: string;
  failed: boolean;
  retried: boolean;
}

export function MediaImage({ media, variant }: MediaImageProps) {
  const { t } = useTranslation('media');
  const key = `${variant}:${media.ref}`;
  const [state, setState] = useState<Shown>({ key, failed: false, retried: false });
  const cur: Shown = state.key === key ? state : { key, failed: false, retried: false };

  useEffect(() => {
    let live = true;
    signedURL(media.ref, variant).then(
      (u) => {
        if (live) setState({ key, src: u.url, failed: false, retried: false });
      },
      () => {
        if (live) setState({ key, failed: true, retried: false });
      },
    );
    return () => {
      live = false;
    };
  }, [key, media.ref, variant]);

  const onError = () => {
    if (cur.retried) {
      setState({ ...cur, failed: true });
      return;
    }
    setState({ ...cur, retried: true });
    refresh(media.ref, variant).then(
      (u) => {
        setState((s) => (s.key === key ? { ...s, src: u.url } : s));
      },
      () => {
        setState((s) => (s.key === key ? { ...s, failed: true } : s));
      },
    );
  };

  const alt = media.alt ?? t('image.noAlt');
  if (cur.failed) {
    return (
      <span className="media-missing" role="img" aria-label={alt} data-testid="media-missing">
        {t('image.unavailable')}
      </span>
    );
  }
  return (
    <img
      className={`media-image media-${variant}`}
      data-testid="media-image"
      data-variant={variant}
      src={cur.src}
      alt={alt}
      width={media.w}
      height={media.h}
      loading="lazy"
      decoding="async"
      referrerPolicy="no-referrer"
      onError={cur.src ? onError : undefined}
      onLoad={() => {
        if (cur.retried) setState((s) => (s.key === key ? { ...s, retried: false } : s));
      }}
    />
  );
}
