// Settings → Passkeys (§17.2, D27): list, add (fresh step-up, never twice on
// one authenticator) and remove (never the last; it signs out every session
// that passkey started). From Phase 1 a session started with a passkey
// co-signs both changes with an existing passkey (passkeyFlow.ts).
// Owner: P0-W01 → P1-W07.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router';
import { Loading } from '../../app/Loading';
import { sessionKey, useSession } from '../../app/session';
import { listPasskeys, logout } from './api';
import { addPasskey, removeKey } from './passkeyFlow';
import { authError, toPasskeyVM, type PasskeyVM } from './vm';
import './auth.css';
import { InfoTip } from '../../lib/InfoTip';
import { SettingsSubNav } from '../../app/SettingsSubNav';

export const passkeysKey = ['auth', 'passkeys'] as const;

function ErrorLine({ err }: { err: unknown }) {
  const { t } = useTranslation(['auth', 'errors']);
  const msg = err ? authError(err) : undefined;
  if (!msg) return null;
  return (
    <p className="auth-error" role="alert">
      {t(msg.key as 'errors:unknown', msg.params)}
    </p>
  );
}

function PasskeyItem({ p, only, onRemove, busy }: { p: PasskeyVM; only: boolean; onRemove: () => void; busy: boolean }) {
  const { t } = useTranslation('auth');
  const name = p.name || t('passkeys.unnamed');
  return (
    <li className="auth-passkey" data-testid="passkey-item">
      <div>
        <strong>{name}</strong>
        {p.current ? <span className="auth-badge">{t('passkeys.current')}</span> : null}
        <div className="muted">
          {t('passkeys.created', { date: p.createdAt })}
          {' · '}
          {p.lastUsedAt ? t('passkeys.lastUsed', { date: p.lastUsedAt }) : t('passkeys.neverUsed')}
        </div>
      </div>
      <button
        type="button"
        data-testid="passkey-remove"
        disabled={only || busy}
        aria-label={t('passkeys.removeLabel', { name })}
        onClick={onRemove}
      >
        {t('passkeys.remove')}
      </button>
    </li>
  );
}

function Passkeys() {
  const { t } = useTranslation('auth');
  const qc = useQueryClient();
  const navigate = useNavigate();
  const nameId = useId();
  const [name, setName] = useState('');
  const [notice, setNotice] = useState<'added' | 'removed' | undefined>();
  const list = useQuery({ queryKey: passkeysKey, queryFn: async () => (await listPasskeys()).map(toPasskeyVM) });

  const add = useMutation({
    mutationFn: async (onPhone: boolean) => {
      await addPasskey(name, t('cosign.addSummary', { name: name.trim() || t('passkeys.unnamed') }), onPhone);
    },
    onMutate: () => {
      setNotice(undefined);
    },
    onSuccess: async () => {
      setName('');
      setNotice('added');
      await qc.invalidateQueries({ queryKey: passkeysKey });
    },
  });

  const remove = useMutation({
    mutationFn: async (p: PasskeyVM) => {
      // A session started with a passkey lists that passkey as current.
      const mustCosign = (list.data ?? []).some((x) => x.current);
      await removeKey(p.id, mustCosign, t('cosign.removeSummary', { name: p.name || t('passkeys.unnamed') }));
      return p;
    },
    onMutate: () => {
      setNotice(undefined);
    },
    onSuccess: async (p) => {
      if (p.current) {
        // This session was started with that passkey: it has ended.
        qc.setQueryData(sessionKey, { authenticated: false, kind: 'none' });
        await navigate('/login', { replace: true });
        return;
      }
      setNotice('removed');
      await qc.invalidateQueries({ queryKey: passkeysKey });
    },
  });

  const signOut = useMutation({
    mutationFn: logout,
    onSuccess: async () => {
      // Leave first: this page sends signed-out visitors to the login page.
      await navigate('/', { replace: true });
      qc.setQueryData(sessionKey, { authenticated: false, kind: 'none' });
    },
  });

  if (list.isPending) return <Loading />;
  const items = list.data ?? [];
  const only = items.length < 2;
  return (
    <>
      <ErrorLine err={list.error} />
      <ul className="auth-list" aria-label={t('passkeys.listLabel')}>
        {items.map((p) => (
          <PasskeyItem
            key={p.id}
            p={p}
            only={only}
            busy={remove.isPending}
            onRemove={() => {
              if (globalThis.confirm(t('passkeys.confirmRemove', { name: p.name || t('passkeys.unnamed') }))) {
                remove.mutate(p);
              }
            }}
          />
        ))}
      </ul>
      {only ? <p className="muted">{t('passkeys.lastHint')}</p> : null}
      <ErrorLine err={remove.error} />
      <form
        className="auth-form"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate(false);
        }}
      >
        <h2>{t('passkeys.addTitle')}</h2>
        <p className="muted">{t('passkeys.addHint')}</p>
        <div className="auth-field">
          <label htmlFor={nameId}>{t('passkeys.nameLabel')}</label>
          <input
            id={nameId}
            type="text"
            maxLength={50}
            value={name}
            placeholder={t('passkeys.namePlaceholder')}
            onChange={(e) => {
              setName(e.target.value);
            }}
          />
        </div>
        <div className="auth-actions">
          <button type="submit" className="primary" data-testid="passkey-add" disabled={add.isPending}>
            {add.isPending && !add.variables ? t('passkeys.adding') : t('passkeys.add')}
          </button>
          <button
            type="button"
            data-testid="passkey-add-phone"
            disabled={add.isPending}
            onClick={() => {
              add.mutate(true);
            }}
          >
            {add.isPending && add.variables ? t('passkeys.adding') : t('passkeys.addPhone')}
          </button>
        </div>
        <p className="muted">{t('passkeys.addPhoneHint')}</p>
        <ErrorLine err={add.error} />
      </form>
      {notice ? (
        <p role="status" className="muted">
          {notice === 'added' ? t('passkeys.added') : t('passkeys.removed')}
        </p>
      ) : null}
      <div className="auth-actions auth-signout">
        <button
          type="button"
          data-testid="sign-out"
          disabled={signOut.isPending}
          onClick={() => {
            signOut.mutate();
          }}
        >
          {t('passkeys.signOut')}
        </button>
      </div>
    </>
  );
}

export default function PasskeysPage() {
  const { t } = useTranslation(['auth', 'common']);
  const session = useSession();
  if (session.isPending) return <Loading />;
  if (!session.data?.authenticated) {
    // No redirect: a sign-out from this page must not race into an automatic sign-in.
    return (
      <section className="page auth-page">
        <h1>{t('passkeys.title')}</h1>
        <p>
          <Link to="/login" data-testid="passkeys-login">
            {t('login.title')}
          </Link>
        </p>
      </section>
    );
  }
  return (
    <section className="page auth-page">
      <h1>{t('passkeys.title')}</h1>
      <SettingsSubNav />
      <p>
        {t('passkeys.intro')}
        <InfoTip>{t('passkeys.introTip')}</InfoTip>
      </p>
      <Passkeys />
    </section>
  );
}
