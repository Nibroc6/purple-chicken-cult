// Moderator page for manual account recovery (§10.3, security #14): moderator 1
// opens it, a different moderator approves (each with a fresh passkey
// step-up), and after the 24 h hold the second moderator fetches the one-time
// enrollment link. Owner: P0-W11 → P1-W10.
import { useMutation } from '@tanstack/react-query';
import { useId, useState, type SyntheticEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError } from '../../api/errors';
import { useMe } from '../../app/session';
import { approveRecovery, isRef, openRecovery, recoveryErrorKey, recoveryLink } from './api';
import '../settings/settings.css';

function ErrorLine({ err }: { err: unknown }) {
  const { t } = useTranslation(['modrecovery', 'errors']);
  if (!err) return null;
  const params = err instanceof ApiError && err.retryAfterS !== undefined ? { seconds: err.retryAfterS } : {};
  return (
    <p className="settings-error" role="alert">
      {t(recoveryErrorKey(err) as 'errors:unknown', params)}
    </p>
  );
}

function Step({
  legend,
  label,
  button,
  run,
  children,
}: {
  legend: string;
  label: string;
  button: string;
  run: { mutate: (v: string) => void; isPending: boolean; error: unknown };
  children?: ReactNode;
}) {
  const id = useId();
  const [value, setValue] = useState('');
  const v = value.trim();
  const onSubmit = (e: SyntheticEvent) => {
    e.preventDefault();
    if (isRef(v)) run.mutate(v);
  };
  return (
    <form className="settings-form" onSubmit={onSubmit}>
      <fieldset>
        <legend>{legend}</legend>
        <div className="settings-field">
          <label htmlFor={id}>{label}</label>
          <input
            id={id}
            className="mono"
            value={value}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            onChange={(e) => {
              setValue(e.target.value);
            }}
          />
        </div>
        <div className="settings-actions">
          <button type="submit" disabled={run.isPending || !isRef(v)}>
            {button}
          </button>
        </div>
        <ErrorLine err={run.error} />
        {children}
      </fieldset>
    </form>
  );
}

function RecoverySteps() {
  const { t } = useTranslation('modrecovery');
  const open = useMutation({ mutationFn: openRecovery });
  const approve = useMutation({ mutationFn: approveRecovery });
  const link = useMutation({ mutationFn: recoveryLink });
  return (
    <>
      <p>{t('page.intro')}</p>
      <Step legend={t('request.legend')} label={t('request.subject')} button={t('request.button')} run={open}>
        <small className="muted">{t('request.help')}</small>
        {open.data ? (
          <p role="status">
            {t('request.created')} <code className="mono">{open.data}</code>
          </p>
        ) : null}
      </Step>
      <Step legend={t('approve.legend')} label={t('approve.id')} button={t('approve.button')} run={approve}>
        {approve.isSuccess ? <p role="status">{t('approve.done')}</p> : null}
      </Step>
      <Step legend={t('link.legend')} label={t('link.id')} button={t('link.button')} run={link}>
        {link.data ? (
          <p role="status">
            {t('link.result')} <code className="mono">{link.data}</code>
          </p>
        ) : null}
      </Step>
    </>
  );
}

export default function ModRecoveryPage() {
  const { t } = useTranslation('modrecovery');
  const me = useMe();
  const moderator = me?.role === 'moderator' || me?.role === 'admin';
  return (
    <section className="page">
      <h1>{t('page.title')}</h1>
      {moderator ? <RecoverySteps /> : <p className="muted">{t('page.membersOnly')}</p>}
    </section>
  );
}
