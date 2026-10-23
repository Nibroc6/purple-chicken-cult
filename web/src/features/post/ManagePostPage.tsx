// Managing one's own post: edit (a new version; parents never change), who may
// reference it (with the "detach all" offer when it turns slippery), continue
// it as a thread, delete it. Someone else's post is simply not found here.
// Owner: P0-W05 → P1-W12.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useParams } from 'react-router';
import { Loading } from '../../app/Loading';
import { NotFound } from '../../app/NotFound';
import { useMe } from '../../app/session';
import { ApiError } from '../../api/errors';
import type { PostVM, RefPolicy } from '../../vm/types';
import { deletePost, detachAll, editPost, fetchPost, postKey, setRefPolicy } from './api';
import { Counter, ErrorText, LangSelect, PolicySelect, PostPreview } from './shared';
import { codePoints, HARD_LIMIT } from './vm';
import { InfoTip } from '../../lib/InfoTip';

export default function ManagePostPage() {
  const { t } = useTranslation('post');
  const ref = useParams().ref ?? '';
  const q = useQuery({ queryKey: [...postKey, ref], queryFn: () => fetchPost(ref), retry: false });
  const [deleted, setDeleted] = useState(false);
  if (deleted) {
    return (
      <section className="page post-page">
        <h1>{t('manage.title')}</h1>
        <p role="status" data-testid="post-deleted">
          {t('delete.done')}
        </p>
        <Link to="/compose">{t('manage.newPost')}</Link>
      </section>
    );
  }
  if ((q.error instanceof ApiError && q.error.isNotFound) || (q.data && !q.data.mine)) return <NotFound />;
  if (q.isPending || q.error) {
    return (
      <section className="page post-page">
        <h1>{t('manage.title')}</h1>
        {q.isPending ? <Loading /> : <ErrorText err={q.error} />}
      </section>
    );
  }
  return (
    <ManagePost
      post={q.data}
      onDeleted={() => {
        setDeleted(true);
      }}
    />
  );
}

function ManagePost({ post, onDeleted }: { post: PostVM; onDeleted: () => void }) {
  const { t } = useTranslation('post');
  return (
    <section className="page post-page">
      <h1>{t('manage.title')}</h1>
      <PostPreview post={post} />
      <p className="post-links">
        <Link to={`/p/${post.ref}`}>{t('manage.open')}</Link>
        {post.thread === undefined || post.thread.index === post.thread.total ? (
          <Link to={`/compose?${new URLSearchParams({ parent: post.ref, kind: 'cont' }).toString()}`} data-testid="post-continue">
            {t('manage.continue')}
          </Link>
        ) : null}
      </p>
      <EditForm post={post} />
      <PolicyForm post={post} />
      <DeleteForm post={post} onDeleted={onDeleted} />
    </section>
  );
}

function EditForm({ post }: { post: PostVM }) {
  const { t } = useTranslation('post');
  const me = useMe();
  const qc = useQueryClient();
  const [body, setBody] = useState(post.body?.source ?? '');
  const [lang, setLang] = useState(post.lang);
  const m = useMutation({
    mutationFn: () => editPost(post.ref, body, lang),
    onSuccess: (p) => {
      qc.setQueryData([...postKey, post.ref], p);
    },
  });
  const n = codePoints(body);
  return (
    <form
      className="post-section"
      onSubmit={(e) => {
        e.preventDefault();
        m.mutate();
      }}
    >
      <h2>{t('edit.title')}</h2>
      <label className="post-field" htmlFor="post-edit-body">
        <span>{t('edit.body')}</span>
        <textarea
          id="post-edit-body"
          data-testid="post-edit-body"
          rows={6}
          value={body}
          onChange={(e) => {
            setBody(e.target.value);
          }}
        />
      </label>
      <Counter text={body} />
      <LangSelect
        id="post-edit-lang"
        value={lang}
        languages={me?.settings.languages}
        uiLocale={me?.settings.ui_locale ?? 'en'}
        onChange={setLang}
      />
      <p className="muted">
        {t('edit.parentsFixed')}
        <InfoTip>{t('edit.parentsFixedTip')}</InfoTip>
      </p>
      <button type="submit" data-testid="post-edit-save" disabled={m.isPending || (body.trim() === '' && post.media.length === 0) || n > HARD_LIMIT}>
        {t('edit.save')}
      </button>
      {m.isSuccess ? (
        <p role="status" data-testid="post-edit-saved">
          {t('edit.saved')}
        </p>
      ) : null}
      <ErrorText err={m.error} />
    </form>
  );
}

function PolicyForm({ post }: { post: PostVM }) {
  const { t } = useTranslation('post');
  const qc = useQueryClient();
  const [policy, setPolicy] = useState<RefPolicy>(post.refPolicy);
  const [offer, setOffer] = useState(false);
  const refresh = () => qc.invalidateQueries({ queryKey: [...postKey, post.ref] });
  const save = useMutation({
    mutationFn: () => setRefPolicy(post.ref, policy),
    onSuccess: async (o) => {
      setOffer(o);
      await refresh();
    },
  });
  const detach = useMutation({
    mutationFn: () => detachAll(post.ref),
    onSuccess: async () => {
      setOffer(false);
      await refresh();
    },
  });
  return (
    <form
      className="post-section"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <h2>{t('policyForm.title')}</h2>
      <PolicySelect id="post-policy" value={policy} onChange={setPolicy} />
      <button type="submit" data-testid="post-policy-save" disabled={save.isPending}>
        {t('policyForm.save')}
      </button>
      {save.isSuccess && !offer ? (
        <p role="status" data-testid="post-policy-saved">
          {t('policyForm.saved')}
        </p>
      ) : null}
      {offer ? (
        <div className="post-offer" role="group" aria-label={t('policyForm.offerTitle')} data-testid="post-detach-offer">
          <p>{t('policyForm.offer')}</p>
          <button
            type="button"
            data-testid="post-detach-all"
            disabled={detach.isPending}
            onClick={() => {
              detach.mutate();
            }}
          >
            {t('policyForm.detachAll')}
          </button>
          <button
            type="button"
            onClick={() => {
              setOffer(false);
            }}
          >
            {t('policyForm.keep')}
          </button>
        </div>
      ) : null}
      {detach.isSuccess ? (
        <p role="status" data-testid="post-detached">
          {t('policyForm.detached')}
        </p>
      ) : null}
      <ErrorText err={save.error ?? detach.error} />
    </form>
  );
}

function DeleteForm({ post, onDeleted }: { post: PostVM; onDeleted: () => void }) {
  const { t } = useTranslation('post');
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const m = useMutation({
    mutationFn: () => deletePost(post.ref),
    onSuccess: () => {
      qc.removeQueries({ queryKey: [...postKey, post.ref] });
      onDeleted();
    },
  });
  return (
    <div className="post-section">
      <h2>{t('delete.title')}</h2>
      {confirming ? (
        <div className="post-offer" role="group" aria-label={t('delete.title')}>
          <p>{t('delete.confirm')}</p>
          <button
            type="button"
            data-testid="post-delete-confirm"
            disabled={m.isPending}
            onClick={() => {
              m.mutate();
            }}
          >
            {t('delete.button')}
          </button>
          <button
            type="button"
            onClick={() => {
              setConfirming(false);
            }}
          >
            {t('delete.cancel')}
          </button>
        </div>
      ) : (
        <button
          type="button"
          data-testid="post-delete"
          onClick={() => {
            setConfirming(true);
          }}
        >
          {t('delete.button')}
        </button>
      )}
      <ErrorText err={m.error} />
    </div>
  );
}
