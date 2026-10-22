// The composer (§10.1.5, §10.1.3, §10.7): counter from 280 code points, amber
// past 500, hard stop at 20,000; split into a thread past 500 (editable parts);
// audience and reference policy (slippery default from settings); language
// detected from the text (else the first of the user's languages, then the UI
// locale), with a picker to choose another, "und" available; replies
// and continuations from ?parent=<ref>&kind=ref|cont, with a warning when the
// parent's audience is limited; up to 4 pictures through the media picker
// (P1; uploaded as soon as picked, attached at publish; a thread attaches them
// to its first part; the picker hides itself when media is turned off). Owner: P0-W05 → P1-W12.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router';
import { useMe } from '../../app/session';
import type { Audience, PostVM, Ref, RefPolicy } from '../../vm/types';
import { addOwnPosts } from '../feed/vm';
import { DraftMediaPicker, useDraftMedia, type DraftMedia } from '../media';
import { createPost, createThread, fetchPost, postKey, type DraftIn, type ParentIn } from '../post/api';
import { defaultLang, detectLang } from '../post/lang';
import { AudienceSelect, AutoLang, Counter, ErrorText, PolicySelect, PostPreview } from '../post/shared';
import { codePoints, HARD_LIMIT, hasAudienceLimit, MAX_MEDIA, MAX_PARTS, SOFT_LIMIT } from '../post/vm';
import { joinParts, splitThread } from './split';
import './composer.css';

type Kind = ParentIn['kind'];

function parentFromQuery(params: URLSearchParams): ParentIn | undefined {
  const ref = params.get('parent') ?? '';
  if (!/^[A-Za-z0-9_-]+$/.test(ref)) return undefined;
  return { ref, kind: params.get('kind') === 'cont' ? 'cont' : 'ref' };
}

export default function ComposePage() {
  const { t } = useTranslation('composer');
  const [params] = useSearchParams();
  const parent = parentFromQuery(params);
  const me = useMe();
  const [published, setPublished] = useState<PostVM[] | null>(null);
  const [round, setRound] = useState(0);
  return (
    <section className="page composer-page">
      <h1>{t('page.title')}</h1>
      {published ? (
        <Published
          posts={published}
          onAgain={() => {
            setPublished(null);
            setRound((r) => r + 1);
          }}
        />
      ) : me ? (
        <Composer key={round} parent={parent} me={me} onPublished={setPublished} />
      ) : null}
    </section>
  );
}

interface MeLike {
  settings: { languages: string[]; ui_locale: string; slippery_default: boolean };
}

function Composer({ parent, me, onPublished }: { parent?: ParentIn; me: MeLike; onPublished: (p: PostVM[]) => void }) {
  const { t } = useTranslation('composer');
  const qc = useQueryClient();
  const [body, setBody] = useState('');
  const [parts, setParts] = useState<string[] | null>(null);
  const [chosenLang, setLang] = useState<string | null>(null);
  const [audience, setAudience] = useState<Audience>('connected');
  const [policy, setPolicy] = useState<RefPolicy>(me.settings.slippery_default ? 'nobody' : 'anyone');
  const [noPreview, setNoPreview] = useState(false);
  const pics = useDraftMedia();
  const pq = useQuery({
    queryKey: [...postKey, parent?.ref ?? ''],
    queryFn: () => fetchPost(parent?.ref ?? ''),
    enabled: parent !== undefined,
    retry: false,
  });
  const parentPost = pq.data;
  const blocked = parent !== undefined && (pq.isError || (parentPost !== undefined && !canAttach(parentPost, parent.kind)));
  const detected = detectLang((parts ?? [body]).join('\n'), me.settings.languages);
  const lang = chosenLang ?? detected ?? defaultLang(me.settings.languages, me.settings.ui_locale);
  const draft = (media: Ref[]): DraftIn => ({
    body,
    lang,
    audience,
    refPolicy: policy,
    noPreview,
    parents: parent && parentPost ? [parent] : [],
    media,
  });
  const m = useMutation({
    // Publish waits for pictures still uploading, then attaches them.
    mutationFn: async () => {
      const d = draft(await pics.finalize());
      return parts ? createThread(d, parts) : [await createPost(d)];
    },
    onSuccess: async (ps) => {
      pics.commit(); // the uploads now belong to the post
      addOwnPosts(qc, ps); // D16: shown on top of the feed
      await qc.invalidateQueries({ queryKey: postKey });
      onPublished(ps);
    },
  });
  const n = codePoints(body);
  const texts = parts ?? [body];
  const ready =
    !blocked &&
    !(parent && !parentPost) &&
    texts.length <= MAX_PARTS &&
    texts.every((p) => p.trim() !== '' && codePoints(p) <= HARD_LIMIT);
  return (
    <form
      className="composer"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready) m.mutate();
      }}
    >
      {parent ? <ParentContext parent={parent} post={parentPost} missing={pq.isError} /> : null}
      {parts ? (
        <ThreadEditor parts={parts} onChange={setParts} />
      ) : (
        <>
          <label className="post-field" htmlFor="composer-body">
            <span>{t('body.label')}</span>
            <textarea
              id="composer-body"
              data-testid="composer-body"
              rows={8}
              value={body}
              onChange={(e) => {
                setBody(e.target.value);
              }}
            />
          </label>
          <Counter text={body} testId="composer-counter" />
          {n > SOFT_LIMIT && n <= HARD_LIMIT ? (
            <div className="composer-split-offer">
              <p className="muted">{t('split.offer')}</p>
              <button
                type="button"
                data-testid="composer-split"
                onClick={() => {
                  setParts(splitThread(body));
                }}
              >
                {t('split.button')}
              </button>
            </div>
          ) : null}
        </>
      )}
      <MediaSection pics={pics} />
      <div className="composer-options">
        <AutoLang
          id="composer-lang"
          value={lang}
          detected={chosenLang === null && detected !== null}
          languages={me.settings.languages}
          uiLocale={me.settings.ui_locale}
          onChange={setLang}
        />
        <AudienceSelect id="composer-audience" value={audience} onChange={setAudience} />
        <PolicySelect id="composer-policy" value={policy} onChange={setPolicy} />
        <label className="composer-check">
          <input
            type="checkbox"
            data-testid="composer-no-preview"
            checked={noPreview}
            onChange={(e) => {
              setNoPreview(e.target.checked);
            }}
          />
          <span>{t('noPreview')}</span>
        </label>
      </div>
      <div className="composer-actions">
        {parts ? (
          <button
            type="button"
            data-testid="composer-unsplit"
            onClick={() => {
              setBody(joinParts(parts));
              setParts(null);
            }}
          >
            {t('split.undo')}
          </button>
        ) : null}
        <button type="submit" data-testid="composer-publish" disabled={!ready || m.isPending || pics.failed}>
          {m.isPending && pics.uploading ? t('publish.waiting') : parts ? t('publish.thread', { count: parts.length }) : t('publish.one')}
        </button>
      </div>
      {pics.failed ? (
        <p className="muted" role="note" data-testid="composer-media-failed">
          {t('publish.mediaFailed')}
        </p>
      ) : (
        <ErrorText err={m.error} />
      )}
    </form>
  );
}

// MediaSection: the draft's pictures. Each uploads as soon as it is picked;
// removing one, or leaving without publishing, discards its upload.
function MediaSection({ pics }: { pics: DraftMedia }) {
  return (
    <div className="composer-media" data-testid="composer-media">
      <DraftMediaPicker draft={pics} max={MAX_MEDIA} />
    </div>
  );
}

// canAttach: a reply needs a parent that accepts references from the viewer; a
// continuation needs the viewer's own post.
function canAttach(p: PostVM, kind: Kind): boolean {
  return kind === 'cont' ? p.mine : p.canReference;
}

// i18n-dynamic: composer:parent.
function ParentContext({ parent, post, missing }: { parent: ParentIn; post: PostVM | undefined; missing: boolean }) {
  const { t } = useTranslation('composer');
  if (missing) {
    return (
      <p className="post-error" role="alert" data-testid="composer-parent-missing">
        {t('parent.missing')}
      </p>
    );
  }
  if (!post) return null;
  return (
    <div className="composer-parent" data-testid="composer-parent">
      <p className="muted">{t(`parent.${parent.kind}` as 'parent.ref', { handle: post.author.handle })}</p>
      <PostPreview post={post} />
      {!canAttach(post, parent.kind) ? (
        <p className="post-error" role="alert" data-testid="composer-parent-closed">
          {t(parent.kind === 'cont' ? 'parent.notOwn' : 'parent.closed')}
        </p>
      ) : parent.kind === 'ref' && hasAudienceLimit(post) ? (
        <p className="composer-warning" role="note" data-testid="composer-audience-warning">
          {t('parent.limited')}
        </p>
      ) : null}
    </div>
  );
}

function ThreadEditor({ parts, onChange }: { parts: string[]; onChange: (p: string[]) => void }) {
  const { t } = useTranslation('composer');
  return (
    <ol className="composer-parts" data-testid="composer-parts">
      {parts.length > MAX_PARTS ? (
        <li className="post-error" role="alert">
          {t('split.tooMany', { max: MAX_PARTS })}
        </li>
      ) : null}
      {parts.map((p, i) => (
        <li key={i} className="composer-part">
          <label className="post-field" htmlFor={`composer-part-${String(i)}`}>
            <span>{t('split.part', { index: i + 1, total: parts.length })}</span>
            <textarea
              id={`composer-part-${String(i)}`}
              data-testid="composer-part"
              rows={5}
              value={p}
              onChange={(e) => {
                onChange(parts.map((x, j) => (j === i ? e.target.value : x)));
              }}
            />
          </label>
          <Counter text={p} testId="composer-part-counter" />
        </li>
      ))}
    </ol>
  );
}

function Published({ posts, onAgain }: { posts: PostVM[]; onAgain: () => void }) {
  const { t } = useTranslation('composer');
  const first = posts[0];
  return (
    <div className="composer-published" data-testid="composer-published" role="status">
      <p>{t('published.done', { count: posts.length })}</p>
      {posts.map((p) => (
        <PostPreview key={p.ref} post={p} />
      ))}
      {first ? (
        <p className="post-links">
          <Link to={`/p/${first.ref}`} data-testid="composer-open">
            {t('published.open')}
          </Link>
          <Link to={`/p/${(posts[posts.length - 1] ?? first).ref}/edit`} data-testid="composer-manage">
            {t('published.manage')}
          </Link>
        </p>
      ) : null}
      <button type="button" data-testid="composer-again" onClick={onAgain}>
        {t('published.again')}
      </button>
    </div>
  );
}
