// The share sheet (?share=<post ref> over any page): send a post into one or
// more of the viewer's conversations, with an optional message, or start a
// new conversation with it. Each recipient sees the post only if they could
// already see it; everyone else gets a placeholder (the server checks per
// viewer on every read). Owner: direct messages (contract request dm-1).
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { Modal, useModalParam } from '../../app/Modal';
import { asRef, type Ref } from '../../vm/types';
import { fetchPost } from '../post/api';
import { codePoints } from '../post/vm';
import { PostPreview } from '../post/shared';
import { conversationName, fetchConversations, MaxBody, messagesKey, sendMessage } from './api';
import { shareParam } from './share';
import { ErrorText } from './shared';
import './messages.css';

export function ShareModal() {
  const { t } = useTranslation('messages');
  const [post, , close] = useModalParam(shareParam);
  if (!post) return null;
  return (
    <Modal title={t('share.title')} onClose={close} testId="share-modal">
      <ShareBody key={post} post={asRef(post)} onDone={close} />
    </Modal>
  );
}

function ShareBody({ post, onDone }: { post: Ref; onDone: () => void }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const noteId = useId();
  const preview = useQuery({ queryKey: ['messages', 'share-post', post], queryFn: () => fetchPost(post), retry: false });
  const convs = useQuery({ queryKey: [...messagesKey, 'share-list'], queryFn: () => fetchConversations(), retry: false });
  const [chosen, setChosen] = useState<Ref[]>([]);
  const [note, setNote] = useState('');
  const [failed, setFailed] = useState(0);
  const send = useMutation({
    mutationFn: async () => {
      const results = await Promise.allSettled(chosen.map((c) => sendMessage(c, note.trim(), post)));
      const bad = results.filter((r) => r.status === 'rejected');
      setFailed(bad.length);
      const first = bad[0];
      if (first && bad.length === results.length) throw first.reason;
      return results.length - bad.length;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: messagesKey }),
  });
  const items = (convs.data?.items ?? []).filter((c) => c.state === 'member' && c.canSend);
  const set = new Set<string>(chosen);
  const tooLong = codePoints(note) > MaxBody;
  if (send.isSuccess) {
    return (
      <div data-testid="share-done">
        <p role="status">{t('share.sent', { count: send.data })}</p>
        {failed > 0 ? <p role="alert">{t('share.failed', { count: failed })}</p> : null}
        <div className="messages-actions">
          <button type="button" data-testid="share-close" onClick={onDone}>
            {t('share.done')}
          </button>
        </div>
      </div>
    );
  }
  return (
    <form
      className="share-sheet"
      onSubmit={(e) => {
        e.preventDefault();
        if (chosen.length > 0 && !tooLong) send.mutate();
      }}
    >
      {preview.data ? <PostPreview post={preview.data} /> : preview.error ? <ErrorText err={preview.error} /> : <Loading />}
      <p className="muted">{t('share.audience')}</p>
      <fieldset className="share-targets">
        <legend>{t('share.pick')}</legend>
        {convs.isPending ? (
          <Loading />
        ) : convs.error ? (
          <ErrorText err={convs.error} />
        ) : items.length === 0 ? (
          <p className="muted">{t('share.none')}</p>
        ) : (
          <ul className="messages-picker-list">
            {items.map((c) => {
              const on = set.has(c.ref);
              return (
                <li key={c.ref}>
                  <label className="messages-picker-person" data-testid="share-target">
                    <input
                      type="checkbox"
                      checked={on}
                      onChange={() => {
                        setChosen(on ? chosen.filter((r) => r !== c.ref) : [...chosen, c.ref]);
                      }}
                    />
                    <span data-user-content="">{conversationName(c, t)}</span>
                  </label>
                </li>
              );
            })}
          </ul>
        )}
        <Link to={`/messages/new?post=${encodeURIComponent(post)}`} data-testid="share-new">
          {t('share.newConversation')}
        </Link>
      </fieldset>
      <p className="messages-field">
        <label htmlFor={noteId}>{t('share.note')}</label>
        <textarea
          id={noteId}
          name="note"
          rows={2}
          data-testid="share-note"
          value={note}
          onChange={(e) => {
            setNote(e.target.value);
          }}
        />
      </p>
      <div className="messages-actions">
        <button type="submit" data-testid="share-send" disabled={chosen.length === 0 || tooLong || send.isPending}>
          {send.isPending ? t('share.sending') : t('share.send')}
        </button>
      </div>
      <ErrorText err={send.error} />
    </form>
  );
}
