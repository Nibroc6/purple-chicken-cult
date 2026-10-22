// Shared pieces of the post and composer features: error text, the length
// counter, the language / audience / reference-policy pickers and a compact
// preview of a post. Owner: P0-W05 → P1-W12.
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AudienceBadge, EditedMark } from '../../integrity';
import { Markdown } from '../../markdown';
import { MediaImage } from '../media';
import type { Audience, PostVM, RefPolicy } from '../../vm/types';
import { langName, langOptions, UND } from './lang';
import { audiences, codePoints, defaultAudience, counterState, errorParams, HARD_LIMIT, postErrorKey, refPolicies, SOFT_LIMIT } from './vm';
import './post.css';

// ErrorText renders a posts API error from the post or errors catalogs.
export function ErrorText({ err }: { err: unknown }) {
  const { t } = useTranslation(['post', 'errors']);
  if (!err) return null;
  return (
    <p className="post-error" role="alert">
      {t(postErrorKey(err) as 'errors:unknown', errorParams(err))}
    </p>
  );
}

// Counter shows the length in code points from 280 on: amber past the soft
// limit (500), an error past the hard cap (20,000).
// i18n-dynamic: post:counter.
export function Counter({ text, testId = 'post-counter' }: { text: string; testId?: string }) {
  const { t } = useTranslation('post');
  const n = codePoints(text);
  const state = counterState(n);
  if (state === 'hidden') return null;
  return (
    <p className={`post-counter post-counter-${state}`} data-testid={testId} data-state={state} aria-live="polite">
      {t(`counter.${state}` as 'counter.normal', { count: n, soft: SOFT_LIMIT, hard: HARD_LIMIT })}
    </p>
  );
}

// LangSelect picks the post language.
export function LangSelect({
  value,
  languages,
  uiLocale,
  onChange,
  id,
}: {
  value: string;
  languages: readonly string[] | undefined;
  uiLocale: string;
  onChange: (lang: string) => void;
  id: string;
}) {
  const { t } = useTranslation('post');
  return (
    <label className="post-field" htmlFor={id}>
      <span>{t('field.lang')}</span>
      <select
        id={id}
        data-testid="post-lang"
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
      >
        {langOptions(languages, value).map((l) => (
          <option key={l} value={l} data-intl={l === UND ? undefined : ''}>
            {l === UND ? t('lang.und') : langName(l, uiLocale)}
          </option>
        ))}
      </select>
    </label>
  );
}

// AutoLang shows the language the composer picked from the text itself, with a
// way to choose another; once opened, the picker stays and its choice sticks.
export function AutoLang({
  value,
  detected,
  languages,
  uiLocale,
  onChange,
  id,
}: {
  value: string;
  detected: boolean;
  languages: readonly string[] | undefined;
  uiLocale: string;
  onChange: (lang: string) => void;
  id: string;
}) {
  const { t } = useTranslation('post');
  const [open, setOpen] = useState(false);
  if (open) return <LangSelect id={id} value={value} languages={languages} uiLocale={uiLocale} onChange={onChange} />;
  const name = value === UND ? t('lang.und') : langName(value, uiLocale);
  return (
    <p className="post-lang-auto" data-testid="post-lang-auto" data-lang={value}>
      <span>{detected ? t('lang.detected', { name }) : t('lang.assumed', { name })}</span>{' '}
      <button
        type="button"
        data-testid="post-lang-change"
        onClick={() => {
          setOpen(true);
        }}
      >
        {t('lang.change')}
      </button>
    </p>
  );
}

// i18n-dynamic: post:audience.
export function AudienceSelect({ value, onChange, id }: { value: Audience; onChange: (a: Audience) => void; id: string }) {
  const { t } = useTranslation('post');
  return (
    <label className="post-field" htmlFor={id}>
      <span>{t('field.audience')}</span>
      <select
        id={id}
        data-testid="post-audience"
        value={value}
        onChange={(e) => {
          onChange(e.target.value as Audience);
        }}
      >
        {audiences.map((a) => (
          <option key={a} value={a}>
            {a === defaultAudience
              ? t('audience.default', { label: t(`audience.${a}` as 'audience.connected') })
              : t(`audience.${a}` as 'audience.connected')}
          </option>
        ))}
      </select>
    </label>
  );
}

// i18n-dynamic: post:policy.
export function PolicySelect({ value, onChange, id }: { value: RefPolicy; onChange: (p: RefPolicy) => void; id: string }) {
  const { t } = useTranslation('post');
  return (
    <label className="post-field" htmlFor={id}>
      <span>{t('field.policy')}</span>
      <select
        id={id}
        data-testid="post-policy"
        value={value}
        onChange={(e) => {
          onChange(e.target.value as RefPolicy);
        }}
      >
        {refPolicies.map((p) => (
          <option key={p} value={p}>
            {t(`policy.${p}` as 'policy.anyone')}
          </option>
        ))}
      </select>
    </label>
  );
}

// PostPreview is a compact, read-only rendering of one post with its audience
// badge and edited mark (integrity elements), visible context rows and its
// pictures as thumbs.
// i18n-dynamic: post:context.
export function PostPreview({ post }: { post: PostVM }) {
  const { t } = useTranslation('post');
  return (
    <article className="post-preview" data-testid="post-preview">
      <header className="post-preview-head">
        <span className="post-handle">{t('handle', { handle: post.author.handle })}</span>
        {post.thread ? (
          <span className="muted" data-testid="post-thread">
            {t('thread.position', { index: post.thread.index, total: post.thread.total })}
          </span>
        ) : null}
      </header>
      {post.context.map((c) => (
        <p key={c.ref} className="post-context muted" data-testid="post-context">
          {t(`context.${c.kind}` as 'context.ref', { handle: c.authorHandle })}{' '}
          <span data-user-content="">{c.excerpt}</span>
        </p>
      ))}
      {post.body ? <Markdown doc={post.body} /> : null}
      {post.media.length > 0 ? (
        <div className="post-preview-media" data-testid="post-preview-media">
          {post.media.map((m) => (
            <MediaImage key={m.ref} media={m} variant="thumb" />
          ))}
        </div>
      ) : null}
      <footer className="post-preview-meta">
        <AudienceBadge audience={post.audience} forced={post.audienceForced} />
        <EditedMark {...(post.editedAt ? { editedAt: post.editedAt } : {})} />
        {post.refPolicy === 'nobody' ? <span className="muted">{t('repliesOff')}</span> : null}
      </footer>
    </article>
  );
}
