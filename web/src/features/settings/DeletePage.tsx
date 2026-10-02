// Account deletion (§10.2): fresh step-up plus typing the handle. The server
// signs every session out at once; the client drops its session and goes home.
// Owner: P0-W11 → P1-W10.
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useId, useState, type SyntheticEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { setCSRF } from '../../api/client';
import type { MeView } from '../../app/session';
import { deleteAccount } from './api';
import { ErrorText, SettingsShell } from './shared';
import { handleMatches } from './vm';

function DeleteForm({ me }: { me: MeView }) {
  const { t } = useTranslation('settings');
  const id = useId();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [typed, setTyped] = useState('');
  const run = useMutation({
    mutationFn: () => deleteAccount(typed.trim()),
    onSuccess: async () => {
      setCSRF(undefined);
      await qc.resetQueries();
      await navigate('/', { replace: true });
    },
  });
  const matches = handleMatches(typed, me.handle);
  const onSubmit = (e: SyntheticEvent) => {
    e.preventDefault();
    if (matches) run.mutate();
  };
  return (
    <form className="settings-form settings-danger" onSubmit={onSubmit}>
      <p>{t('delete.warning')}</p>
      <div className="settings-field">
        <label htmlFor={`${id}-confirm`}>{t('delete.confirmLabel', { handle: me.handle })}</label>
        <input
          id={`${id}-confirm`}
          value={typed}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          onChange={(e) => {
            setTyped(e.target.value);
          }}
        />
      </div>
      <div className="settings-actions">
        <button type="submit" disabled={run.isPending || !matches}>
          {run.isPending ? t('delete.working') : t('delete.button')}
        </button>
      </div>
      <ErrorText err={run.error} />
    </form>
  );
}

export default function DeletePage() {
  const { t } = useTranslation('settings');
  return <SettingsShell title={t('delete.title')}>{(me) => <DeleteForm me={me} />}</SettingsShell>;
}
