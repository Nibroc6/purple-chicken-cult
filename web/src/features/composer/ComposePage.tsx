// The composer (§10.1.5, §10.1.3, §10.7): counter from 280 code points, amber
// past 500, hard stop at 20,000; split into a thread past 500 (editable parts);
// audience and reference policy (slippery default from settings); language
// detected from the text (else the first of the user's languages, then the UI
// locale), with a picker to choose another, "und" available; replies
// and continuations from ?parent=<ref>&kind=ref|cont, with a warning when a
// parent's audience is limited; a reply to several posts (up to 3) from
// repeated ?parent=, more added by pasting a post's link, any taken out; up to
// 4 pictures through the media picker
// (P1; uploaded as soon as picked, attached at publish; a thread attaches them
// to its first part; the picker hides itself when media is turned off). Owner: P0-W05 → P1-W12.
import { useMutation, useQueries, useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { useMe } from '../../app/session';
import type { Audience, PostVM, Ref, RefPolicy } from '../../vm/types';
import { addOwnPosts } from '../feed/vm';
import { DraftMediaPicker, useDraftMedia, type DraftMedia } from '../media';
import { createPost, createThread, fetchPost, postKey, type DraftIn, type ParentIn } from '../post/api';
import { defaultLang, detectLang } from '../post/lang';
import { AudienceSelect, AutoLang, Counter, ErrorText, PolicySelect, PostPreview } from '../post/shared';
import { codePoints, defaultAudience, HARD_LIMIT, hasAudienceLimit, MAX_MEDIA, MAX_PARENTS, MAX_PARTS, SOFT_LIMIT } from '../post/vm';
import { REF, refFromLink } from './picks';
import { clearDraft, loadDraft, saveDraft } from './savedDraft';
import { joinParts, splitThread } from './split';
import './composer.css';

type Kind = ParentIn['kind'];

// parentsFromQuery reads ?parent= (repeatable, up to MAX_PARENTS distinct
// posts) and ?kind=; a continuation has exactly one parent.
function parentsFromQuery(params: URLSearchParams): ParentIn[] {
  const refs = [...new Set(params.getAll('parent').filter((r) => REF.test(r)))];
  if (params.get('kind') === 'cont') return refs.length === 1 ? [{ ref: refs[0] ?? '', kind: 'cont' }] : [];
  return refs.slice(0, MAX_PARENTS).map((ref) => ({ ref, kind: 'ref' }));
}

export default function ComposePage() {
  const { t } = useTranslation('composer');
  const [params] = useSearchParams();
  const parents = parentsFromQuery(params);
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
        <Composer key={round} query={params.toString()} initialParents={parents} me={me} onPublished={setPublished} />
      ) : null}
    </section>
  );
}

interface MeLike {
  settings: { languages: string[]; ui_locale: string; slippery_default: boolean };
}

function Composer({
  query,
  initialParents,
  me,
  onPublished,
}: {
  query: string;
  initialParents: ParentIn[];
  me: MeLike;
  onPublished: (p: PostVM[]) => void;
}) {
  const { t } = useTranslation('composer');
  const qc = useQueryClient();
  const navigate = useNavigate();
  // A reply left to read a post it replies to comes back as it was (its
  // pictures excepted: their uploads end with the page).
  const keep = initialParents.length > 0;
  const [saved] = useState(() => (keep ? loadDraft(query) : {}));
  const [parents, setParents] = useState(saved.parents ?? initialParents);
  const [body, setBody] = useState(saved.body ?? '');
  const [parts, setParts] = useState<string[] | null>(saved.parts ?? null);
  const [chosenLang, setLang] = useState<string | null>(saved.lang ?? null);
  const [audience, setAudience] = useState<Audience>(saved.audience ?? defaultAudience);
  const [policy, setPolicy] = useState<RefPolicy>(saved.policy ?? (me.settings.slippery_default ? 'nobody' : 'anyone'));
  const [noPreview, setNoPreview] = useState(saved.noPreview ?? false);
  const pics = useDraftMedia();
  useEffect(() => {
    if (keep) saveDraft(query, { parents, body, parts, lang: chosenLang, audience, policy, noPreview });
  }, [keep, query, parents, body, parts, chosenLang, audience, policy, noPreview]);
  const pqs = useQueries({
    queries: parents.map((p) => ({
      queryKey: [...postKey, p.ref],
      queryFn: () => fetchPost(p.ref),
      retry: false,
    })),
  });
  const blocked = parents.some((p, i) => {
    const q = pqs[i];
    return q?.isError === true || (q?.data !== undefined && !canAttach(q.data, p.kind));
  });
  const loading = pqs.some((q) => q.data === undefined && !q.isError);
  const detected = detectLang((parts ?? [body]).join('\n'), me.settings.languages);
  const lang = chosenLang ?? detected ?? defaultLang(me.settings.languages, me.settings.ui_locale);
  const draft = (media: Ref[]): DraftIn => ({
    body,
    lang,
    audience,
    refPolicy: policy,
    noPreview,
    parents,
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
      clearDraft(query);
      onPublished(ps);
    },
  });
  const n = codePoints(body);
  const texts = parts ?? [body];
  const ready =
    !blocked &&
    !loading &&
    texts.length <= MAX_PARTS &&
    // A single post with pictures needs no text.
    texts.every((p) => (p.trim() !== '' || (!parts && pics.items.length > 0)) && codePoints(p) <= HARD_LIMIT);
  return (
    <form
      className="composer"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready) m.mutate();
      }}
    >
      {parents.length > 0 ? (
        <div className="composer-parents" data-testid="composer-parents" data-count={parents.length}>
          {parents.length > 1 ? <p className="muted">{t('parent.refMany', { count: parents.length })}</p> : null}
          {parents.map((p, i) => (
            <ParentContext
              key={p.ref}
              parent={p}
              post={pqs[i]?.data}
              missing={pqs[i]?.isError === true}
              many={parents.length > 1}
              onOpen={(ref) => {
                void navigate(`/p/${ref}`);
              }}
              onRemove={
                parents.length > 1
                  ? () => {
                      setParents(parents.filter((x) => x.ref !== p.ref));
                    }
                  : undefined
              }
            />
          ))}
        </div>
      ) : null}
      {parents.every((p) => p.kind === 'ref') && parents.length > 0 ? (
        <AddParent
          count={parents.length}
          onAdd={(ref) => {
            if (!parents.some((x) => x.ref === ref)) setParents([...parents, { ref, kind: 'ref' }]);
          }}
        />
      ) : null}
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
      <div className="composer-options" data-testid="composer-options">
        <AudienceSelect id="composer-audience" value={audience} onChange={setAudience} />
        <PolicySelect id="composer-policy" value={policy} onChange={setPolicy} />
        <AutoLang
          id="composer-lang"
          value={lang}
          detected={chosenLang === null && detected !== null}
          languages={me.settings.languages}
          uiLocale={me.settings.ui_locale}
          onChange={setLang}
        />
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
function ParentContext({
  parent,
  post,
  missing,
  many,
  onOpen,
  onRemove,
}: {
  parent: ParentIn;
  post: PostVM | undefined;
  missing: boolean;
  many: boolean;
  onOpen: (ref: PostVM['ref']) => void;
  onRemove?: (() => void) | undefined;
}) {
  const { t } = useTranslation('composer');
  const remove = onRemove ? (
    <button type="button" data-testid="composer-parent-remove" onClick={onRemove}>
      {post ? t('parent.remove', { handle: post.author.handle }) : t('parent.removeMissing')}
    </button>
  ) : null;
  if (missing) {
    return (
      <div className="composer-parent">
        <p className="post-error" role="alert" data-testid="composer-parent-missing">
          {t('parent.missing')}
        </p>
        {remove}
      </div>
    );
  }
  if (!post) return null;
  return (
    <div className="composer-parent" data-testid="composer-parent" data-ref={parent.ref}>
      {many ? null : <p className="muted">{t(`parent.${parent.kind}` as 'parent.ref', { handle: post.author.handle })}</p>}
      <PostPreview post={post} onOpen={onOpen} />
      {!canAttach(post, parent.kind) ? (
        <p className="post-error" role="alert" data-testid="composer-parent-closed">
          {t(parent.kind === 'cont' ? 'parent.notOwn' : 'parent.closed')}
        </p>
      ) : parent.kind === 'ref' && hasAudienceLimit(post) ? (
        <p className="composer-warning" role="note" data-testid="composer-audience-warning">
          {t('parent.limited')}
        </p>
      ) : null}
      {remove}
    </div>
  );
}

// AddParent adds another post to reply to, from a pasted link to it, while
// fewer than MAX_PARENTS are chosen.
function AddParent({ count, onAdd }: { count: number; onAdd: (ref: string) => void }) {
  const { t } = useTranslation('composer');
  const [text, setText] = useState('');
  const [bad, setBad] = useState(false);
  if (count >= MAX_PARENTS) {
    return (
      <p className="muted" data-testid="composer-parent-max">
        {t('parent.max', { max: MAX_PARENTS })}
      </p>
    );
  }
  const add = () => {
    const ref = refFromLink(text);
    setBad(ref === undefined);
    if (ref === undefined) return;
    onAdd(ref);
    setText('');
  };
  return (
    <div className="composer-add-parent">
      <label className="post-field" htmlFor="composer-add-parent">
        <span>{t('parent.add')}</span>
        <input
          id="composer-add-parent"
          data-testid="composer-add-parent"
          type="text"
          inputMode="url"
          placeholder={t('parent.addHint')}
          value={text}
          aria-invalid={bad}
          onChange={(e) => {
            setText(e.target.value);
            setBad(false);
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              add();
            }
          }}
        />
      </label>
      <button type="button" data-testid="composer-add-parent-button" disabled={text.trim() === ''} onClick={add}>
        {t('parent.addButton')}
      </button>
      {bad ? (
        <p className="post-error" role="alert" data-testid="composer-add-parent-bad">
          {t('parent.badLink')}
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
