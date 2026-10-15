// LinkPreview renders one server-built preview (ARCHITECTURE §10.1.6): the real
// host is the only trusted field and is always shown, decoded, with the
// mixed-script warning; title and description are user content. The href is
// protocol-checked and stripped of tracking parameters like every other link.
// Layout only: a PreviewVM in, no data fetching.
import { useTranslation } from 'react-i18next';
import type { PreviewVM } from '../vm/types';
import { displayHost, safeLink } from './host';
import './markdown.css';

export function LinkPreview({ preview }: { preview: PreviewVM }) {
  const { t } = useTranslation('markdown');
  const link = safeLink(preview.url);
  // The server's host fields (urlx.DisplayHost) win; the URL is the fallback.
  const host =
    preview.host !== ''
      ? { unicode: preview.hostUnicode || displayHost(preview.host).unicode, lookalike: preview.lookalike }
      : link?.host;
  const hostText = host ? (host.lookalike ? t('link.lookalike', { host: host.unicode }) : host.unicode) : '';
  const body = (
    <>
      <span className="md-preview-host" data-testid="md-preview-host" data-lookalike={host?.lookalike ?? false}>
        {hostText}
      </span>
      {preview.title ? (
        <strong className="md-preview-title" data-user-content="">
          {preview.title}
        </strong>
      ) : null}
      {preview.description ? (
        <span className="md-preview-description" data-user-content="">
          {preview.description}
        </span>
      ) : null}
    </>
  );
  return (
    <aside className="md-preview" data-testid="md-preview" aria-label={t('preview.label', { host: host?.unicode ?? '' })}>
      {link ? (
        <a href={link.href} rel="noopener noreferrer nofollow ugc" referrerPolicy="no-referrer" target="_blank">
          {body}
        </a>
      ) : (
        body
      )}
    </aside>
  );
}

// LinkPreviews renders a post's previews in order.
export function LinkPreviews({ previews }: { previews: readonly PreviewVM[] }) {
  if (previews.length === 0) return null;
  return (
    <div className="md-previews">
      {previews.map((p) => (
        <LinkPreview key={p.url} preview={p} />
      ))}
    </div>
  );
}
