// Start a conversation: choose one connection for a private conversation or
// several for a group (named or not). ?post=<ref> sends that post along as
// the first message (the share sheet's "start a new conversation"). Owner:
// direct messages (contract request dm-1).
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { asRef, type Ref } from '../../vm/types';
import { createGroup, MaxMembers, MaxTitle, messagesKey, openDirect, sendMessage } from './api';
import { ErrorText, PeoplePicker } from './shared';
import './messages.css';

export default function NewConversationPage() {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const post = params.get('post');
  const titleId = useId();
  const [chosen, setChosen] = useState<Ref[]>([]);
  const [title, setTitle] = useState('');
  const group = chosen.length > 1;
  const start = useMutation({
    mutationFn: async () => {
      const [first] = chosen;
      if (!first) throw new Error('nobody chosen');
      const conv = group ? await createGroup(title.trim(), chosen) : await openDirect(first);
      if (post) await sendMessage(conv.ref, '', asRef(post));
      return conv;
    },
    onSuccess: (conv) => {
      void qc.invalidateQueries({ queryKey: messagesKey });
      void navigate(`/messages/${conv.ref}`, { replace: true });
    },
  });
  return (
    <section className="page messages-page">
      <p>
        <Link to="/messages">{t('conv.back')}</Link>
      </p>
      <h1>{t('start.title')}</h1>
      <p className="muted">{t('start.hint')}</p>
      {post ? <p data-testid="start-sharing">{t('start.sharing')}</p> : null}
      <form
        className="messages-start"
        onSubmit={(e) => {
          e.preventDefault();
          if (chosen.length > 0) start.mutate();
        }}
      >
        <PeoplePicker chosen={chosen} onChange={setChosen} max={MaxMembers - 1} />
        {group ? (
          <p className="messages-field">
            <label htmlFor={titleId}>{t('start.groupTitle')}</label>
            <input
              id={titleId}
              name="title"
              data-testid="start-title"
              maxLength={MaxTitle}
              value={title}
              onChange={(e) => {
                setTitle(e.target.value);
              }}
            />
          </p>
        ) : null}
        <div className="messages-actions messages-start-actions">
          <span className="muted" aria-live="polite">
            {chosen.length > 0 ? t('start.selected', { count: chosen.length }) : null}
          </span>
          <button type="submit" data-testid="start-submit" disabled={chosen.length === 0 || start.isPending}>
            {group ? t('start.create') : t('start.open')}
          </button>
        </div>
        <ErrorText err={start.error} />
      </form>
    </section>
  );
}
