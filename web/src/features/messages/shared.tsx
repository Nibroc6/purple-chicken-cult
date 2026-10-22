// Shared pieces of the messages pages: error text, the people picker, an inline
// confirmation and the shared-post card. Owner: direct messages (contract
// request dm-1).
import { useInfiniteQuery } from '@tanstack/react-query';
import { useId, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import type { PostVM, Ref } from '../../vm/types';
import { fetchFriends } from '../people/api';
import { PostPreview } from '../post/shared';
import { messageErrorKey } from './api';
import './messages.css';

export function ErrorText({ err }: { err: unknown }) {
  const { t } = useTranslation(['messages', 'errors']);
  if (!err) return null;
  return (
    <p className="messages-error" role="alert" data-testid="messages-error">
      {t(messageErrorKey(err) as 'errors:unknown')}
    </p>
  );
}

// Confirm asks once more before a step that cannot be taken back, inline (no
// second modal over the page).
export function Confirm({
  label,
  question,
  onConfirm,
  testId,
  disabled,
  className,
}: {
  label: string;
  question: string;
  onConfirm: () => void;
  testId: string;
  disabled?: boolean;
  className?: string;
}) {
  const { t } = useTranslation('messages');
  const [asking, setAsking] = useState(false);
  if (!asking) {
    return (
      <button
        type="button"
        className={className}
        data-testid={testId}
        disabled={disabled}
        onClick={() => {
          setAsking(true);
        }}
      >
        {label}
      </button>
    );
  }
  return (
    <span className="messages-confirm" role="group" aria-label={question}>
      <span>{question}</span>
      <button
        type="button"
        data-testid={`${testId}-yes`}
        disabled={disabled}
        onClick={() => {
          setAsking(false);
          onConfirm();
        }}
      >
        {t('confirm.yes')}
      </button>
      <button
        type="button"
        data-testid={`${testId}-no`}
        onClick={() => {
          setAsking(false);
        }}
      >
        {t('confirm.no')}
      </button>
    </span>
  );
}

// SharedPost is a post inside a message: the post when this viewer can see
// it, a placeholder (and nothing else about it) when not.
export function SharedPost({ post }: { post: PostVM | 'unavailable' }) {
  const { t } = useTranslation('messages');
  if (post === 'unavailable') {
    return (
      <p className="message-post-unavailable muted" data-testid="message-post-unavailable">
        {t('post.unavailable')}
      </p>
    );
  }
  return (
    <div className="message-post" data-testid="message-post">
      <PostPreview post={post} />
      <Link to={`/p/${post.ref}`} data-testid="message-post-open">
        {t('post.open')}
      </Link>
    </div>
  );
}

// PeoplePicker lists the viewer's connections (acquaintances and friends) to
// choose from, with a filter. exclude leaves out people already there.
export function PeoplePicker({
  chosen,
  onChange,
  exclude,
  max,
  extra,
}: {
  chosen: Ref[];
  onChange: (next: Ref[]) => void;
  exclude?: ReadonlySet<string>;
  max: number;
  extra?: ReactNode;
}) {
  const { t } = useTranslation('messages');
  const filterId = useId();
  const [filter, setFilter] = useState('');
  const q = useInfiniteQuery({
    queryKey: ['messages', 'people'],
    queryFn: ({ pageParam }) => fetchFriends(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
  });
  const all = (q.data?.pages.flatMap((p) => p.items) ?? []).filter((p) => !exclude?.has(p.handle));
  const f = filter.trim().toLowerCase().replace(/^@/, '');
  const shown = f ? all.filter((p) => p.handle.toLowerCase().includes(f) || p.displayName.toLowerCase().includes(f)) : all;
  const set = new Set<string>(chosen);
  return (
    <fieldset className="messages-picker" data-testid="people-picker">
      <legend>{t('start.pick')}</legend>
      <label htmlFor={filterId} className="messages-picker-filter">
        {t('start.search')}
      </label>
      <input
        id={filterId}
        type="search"
        name="filter"
        autoComplete="off"
        data-testid="picker-filter"
        value={filter}
        onChange={(e) => {
          setFilter(e.target.value);
        }}
      />
      {extra}
      {q.isPending ? null : all.length === 0 ? (
        <p className="muted">{t('start.none')}</p>
      ) : shown.length === 0 ? (
        <p className="muted">{t('start.noMatch')}</p>
      ) : (
        <ul className="messages-picker-list">
          {shown.map((p) => {
            const on = set.has(p.ref);
            return (
              <li key={p.ref}>
                <label className="messages-picker-person" data-testid="picker-person" data-handle={p.handle}>
                  <input
                    type="checkbox"
                    checked={on}
                    disabled={!on && chosen.length >= max}
                    onChange={() => {
                      onChange(on ? chosen.filter((r) => r !== p.ref) : [...chosen, p.ref]);
                    }}
                  />
                  <span>
                    {p.displayName ? <strong data-user-content="">{p.displayName}</strong> : null}{' '}
                    <span className="muted">@{p.handle}</span>
                  </span>
                </label>
              </li>
            );
          })}
        </ul>
      )}
      {q.hasNextPage ? (
        <button
          type="button"
          disabled={q.isFetchingNextPage}
          onClick={() => {
            void q.fetchNextPage();
          }}
        >
          {t('start.more')}
        </button>
      ) : null}
      <ErrorText err={q.error} />
    </fieldset>
  );
}
