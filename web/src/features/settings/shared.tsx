// Shared pieces of the settings pages. Owner: P0-W11 → P1-W10.
import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { sessionKey, useMe, useSession, type MeView } from '../../app/session';
import { patchSettings, type SettingsPatch } from './api';
import { accountErrorKey, errorParams } from './vm';
import './settings.css';

// ErrorText renders an API error from the account, errors or common catalogs.
export function ErrorText({ err }: { err: unknown }) {
  const { t } = useTranslation(['account', 'errors']);
  if (!err) return null;
  return (
    <p className="settings-error" role="alert">
      {t(accountErrorKey(err) as 'errors:unknown', errorParams(err))}
    </p>
  );
}

// SettingsShell is the frame of one settings page: title, back link and, once
// the session is known, the page body for the signed-in user.
export function SettingsShell({ title, children }: { title: string; children: (me: MeView) => ReactNode }) {
  const { t } = useTranslation('settings');
  const session = useSession();
  const me = useMe();
  return (
    <section className="page settings-page">
      <h1>{title}</h1>
      <p>
        <Link to="/settings">{t('index.title')}</Link>
      </p>
      {me ? children(me) : session.isPending ? <Loading /> : null}
    </section>
  );
}

// useSaveSettings PATCHes /me/settings and refreshes the session's MeView.
export function useSaveSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (p: SettingsPatch) => patchSettings(p),
    onSuccess: () => qc.invalidateQueries({ queryKey: sessionKey }),
  });
}

// SaveBar is the submit button with the saving / saved / error states.
export function SaveBar({ pending, saved, err }: { pending: boolean; saved: boolean; err: unknown }) {
  const { t } = useTranslation('settings');
  return (
    <div className="settings-actions">
      <button type="submit" disabled={pending}>
        {pending ? t('form.saving') : t('form.save')}
      </button>
      {saved && !pending && !err ? (
        <p role="status" className="muted">
          {t('form.saved')}
        </p>
      ) : null}
      <ErrorText err={err} />
    </div>
  );
}
