// Shared pieces of the settings pages. Owner: P0-W11 → P1-W10.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Loading } from '../../app/Loading';
import { sessionKey, useSession } from '../../app/session';
import { fetchMe, patchSettings, type MeView, type SettingsPatch } from './api';
import { accountErrorKey, errorParams } from './vm';
import './settings.css';
import { SettingsSubNav } from '../../app/SettingsSubNav';

export const accountMeKey = ['account', 'me'] as const;

// useAccountMe is GET /api/me for a signed-in session (idle otherwise).
export function useAccountMe() {
  const session = useSession();
  return useQuery({ queryKey: accountMeKey, queryFn: fetchMe, enabled: session.data?.authenticated === true });
}

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

// SettingsShell is the frame of one settings page: title, section tabs and, once
// the account is loaded, the page body for the signed-in user.
export function SettingsShell({ title, children }: { title: string; children: (me: MeView) => ReactNode }) {
  const session = useSession();
  const me = useAccountMe();
  const loading = session.isPending || (session.data?.authenticated === true && me.isPending);
  return (
    <section className="page settings-page">
      <h1>{title}</h1>
      <SettingsSubNav />
      {me.data ? children(me.data) : loading ? <Loading /> : <ErrorText err={me.error} />}
    </section>
  );
}

// useSaveSettings PATCHes /me/settings and refreshes both views of the user.
export function useSaveSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (p: SettingsPatch) => patchSettings(p),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: accountMeKey }),
        qc.invalidateQueries({ queryKey: sessionKey }),
      ]);
    },
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
