// Moderators and admins (/mod/roles). Admins only: the current team, adding
// and removing moderators, and the latest role changes. Adding or removing
// needs a fresh passkey check (a 401 step_up_required opens the global
// step-up and the change is sent again once verified); every change goes into
// the public moderation log. The admin role changes only through the CLI.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { errorText } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { useMe } from '../../app/session';
import { formatDateTime, formatRelative } from '../../lib/time';
import { withStepUp } from '../stepup/store';
import { fetchStaff, grantModerator, moderationErrorKey, revokeModerator, type RoleChange, type StaffMember } from './api';
import { ModNav } from './ModNav';
import './moderation.css';

export const staffKey = ['moderation', 'staff'] as const;

export default function RolesPage() {
  const { t } = useTranslation('moderation');
  const me = useMe();
  return (
    <section className="page moderation-page" data-testid="mod-roles-page">
      <h1>{t('roles.title')}</h1>
      <ModNav />
      {me?.role === 'admin' ? <Roles /> : <p className="muted">{t('roles.adminsOnly')}</p>}
    </section>
  );
}

function Roles() {
  const { t } = useTranslation(['moderation', 'errors']);
  const q = useQuery({ queryKey: staffKey, queryFn: fetchStaff, retry: false });
  const [notice, setNotice] = useState('');
  return (
    <>
      <p className="muted">{t('roles.intro')}</p>
      {notice ? (
        <p role="status" data-testid="roles-notice">
          {notice}
        </p>
      ) : null}
      {q.isPending ? <Loading /> : null}
      {q.error ? (
        <p className="moderation-error" role="alert">
          {errorText(t, q.error)}
        </p>
      ) : null}
      {q.data ? (
        <>
          <h2>{t('roles.current')}</h2>
          {q.data.members.length === 0 ? <p className="muted">{t('roles.empty')}</p> : null}
          <ul className="moderation-list" data-testid="roles-members">
            {q.data.members.map((m) => (
              <Member key={m.ref} m={m} onDone={setNotice} />
            ))}
          </ul>
          <AddModerator onDone={setNotice} />
          <h2>{t('roles.history')}</h2>
          {q.data.changes.length === 0 ? (
            <p className="muted" data-testid="roles-history-empty">
              {t('roles.historyEmpty')}
            </p>
          ) : (
            <ol className="moderation-list" data-testid="roles-history">
              {q.data.changes.map((c, i) => (
                <Change key={`${c.at}-${String(i)}`} c={c} />
              ))}
            </ol>
          )}
        </>
      ) : null}
    </>
  );
}

function Member({ m, onDone }: { m: StaffMember; onDone: (notice: string) => void }) {
  const { t, i18n } = useTranslation(['moderation', 'errors']);
  const qc = useQueryClient();
  const [asking, setAsking] = useState(false);
  const revoke = useMutation({
    mutationFn: () => withStepUp(() => revokeModerator(m.ref)),
    onSuccess: async () => {
      setAsking(false);
      onDone(t('roles.removed', { handle: m.handle }));
      await qc.invalidateQueries({ queryKey: staffKey });
    },
  });
  const since = m.since ? new Date(m.since) : undefined;
  return (
    <li className="moderation-item" data-testid="roles-member" data-role={m.role}>
      <p className="moderation-meta">
        <strong>{t('log.handle', { handle: m.handle })}</strong>
        {m.display_name ? <span className="muted"> {m.display_name}</span> : null}
        {m.me ? <span className="muted"> {t('roles.you')}</span> : null}
        <span className="moderation-tag">{t('roles.role', { role: m.role })}</span>
      </p>
      {since ? (
        <p className="moderation-meta muted">
          <time dateTime={since.toISOString()} title={formatDateTime(since, i18n.language)}>
            {t('roles.since', { date: formatRelative(since, i18n.language) })}
          </time>
        </p>
      ) : null}
      {m.role === 'moderator' ? (
        asking ? (
          <div className="moderation-actions" data-testid="roles-confirm">
            <p>{t('roles.removeConfirm', { handle: m.handle })}</p>
            <button
              type="button"
              disabled={revoke.isPending}
              data-testid="roles-remove-yes"
              onClick={() => {
                revoke.mutate();
              }}
            >
              {t('roles.removeYes')}
            </button>
            <button
              type="button"
              disabled={revoke.isPending}
              onClick={() => {
                setAsking(false);
                revoke.reset();
              }}
            >
              {t('roles.cancel')}
            </button>
          </div>
        ) : (
          <div className="moderation-actions">
            <button
              type="button"
              data-testid="roles-remove"
              onClick={() => {
                setAsking(true);
              }}
            >
              {t('roles.remove')}
            </button>
          </div>
        )
      ) : null}
      {revoke.error ? (
        <p className="moderation-error" role="alert" data-testid="roles-error">
          {errorText(t, revoke.error, moderationErrorKey)}
        </p>
      ) : null}
    </li>
  );
}

function AddModerator({ onDone }: { onDone: (notice: string) => void }) {
  const { t } = useTranslation(['moderation', 'errors']);
  const qc = useQueryClient();
  const id = useId();
  const [handle, setHandle] = useState('');
  const clean = handle.trim().replace(/^@/, '');
  const grant = useMutation({
    mutationFn: (h: string) => withStepUp(() => grantModerator(h)),
    onSuccess: async (_, h) => {
      setHandle('');
      onDone(t('roles.added', { handle: h }));
      await qc.invalidateQueries({ queryKey: staffKey });
    },
  });
  return (
    <form
      className="moderation-form"
      data-testid="roles-add"
      onSubmit={(e) => {
        e.preventDefault();
        if (clean !== '') grant.mutate(clean);
      }}
    >
      <h2>{t('roles.addTitle')}</h2>
      <div className="moderation-field">
        <label htmlFor={id}>{t('roles.handle')}</label>
        <input
          id={id}
          value={handle}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={64}
          data-testid="roles-handle"
          onChange={(e) => {
            setHandle(e.target.value);
            grant.reset();
          }}
        />
      </div>
      <div className="moderation-actions">
        <button type="submit" disabled={clean === '' || grant.isPending} data-testid="roles-add-submit">
          {t('roles.add')}
        </button>
      </div>
      {grant.error ? (
        <p className="moderation-error" role="alert" data-testid="roles-add-error">
          {errorText(t, grant.error, moderationErrorKey)}
        </p>
      ) : null}
    </form>
  );
}

function Change({ c }: { c: RoleChange }) {
  const { t, i18n } = useTranslation('moderation');
  const at = new Date(c.at);
  return (
    <li className="moderation-item" data-testid="roles-change" data-action={c.action}>
      <p className="moderation-meta">{t('roles.change', { action: c.action, handle: c.handle, by: c.by_handle })}</p>
      <p className="moderation-meta muted">
        <time dateTime={at.toISOString()} title={formatDateTime(at, i18n.language)}>
          {formatRelative(at, i18n.language)}
        </time>
      </p>
    </li>
  );
}
