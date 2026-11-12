// Sign in with a passkey (§17.2; plan §6.4). The page offers passkey autofill
// (conditional mediation) on the input and a button for the regular prompt.
// Every attempt is its own server ceremony in a fresh guest session; a
// successful one replaces it with a new user session. Owner: P0-W01 → P1-W07.
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router';
import { Loading } from '../../app/Loading';
import { sessionKey, useSession } from '../../app/session';
import { loginBegin, loginFinish, type SessionView } from './api';
import { authError, type ErrorMsg } from './vm';
import { autofillSupported, cancelCeremony, getAssertion } from './webauthn';
import './auth.css';

// safeNext is where to go after signing in: a same-site path from ?next=,
// else the feed.
export function safeNext(next: string | null): string {
  return next && next.startsWith('/') && !next.startsWith('//') && !next.startsWith('/\\') ? next : '/';
}

interface Autofill {
  begun: Promise<unknown>;
  superseded: boolean;
}

export default function LoginPage() {
  const { t } = useTranslation(['auth', 'errors']);
  const session = useSession();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const next = safeNext(params.get('next'));
  const inputId = useId();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<ErrorMsg | undefined>();
  // The autofill ceremony: a button press waits for its begin, then supersedes it.
  const autofill = useRef<Autofill | null>(null);
  const signedOut = session.data?.authenticated === false;

  const doneRef = useRef(async (sv: SessionView) => {
    qc.setQueryData(sessionKey, sv);
    await navigate(safeNext(new URLSearchParams(window.location.search).get('next')), { replace: true });
  });

  useEffect(() => {
    if (!signedOut) return;
    const flags = { live: true };
    const state: Autofill = { begun: Promise.resolve(), superseded: false };
    autofill.current = state;
    // stale is re-read after every await (the page may unmount or a button press may take over).
    const stale = () => !flags.live || state.superseded;
    const run = async () => {
      if (!(await autofillSupported()) || stale()) return;
      const begun = loginBegin();
      state.begun = begun.catch(() => undefined);
      try {
        const options = await begun;
        if (stale()) return;
        let credential;
        try {
          credential = await getAssertion(options, true);
        } catch {
          // The background autofill request failing (no passkey offered, or
          // a browser without one) is not the reader's mistake: stay quiet.
          return;
        }
        if (stale()) return;
        await doneRef.current(await loginFinish(credential));
      } catch (e) {
        if (!stale()) setErr(authError(e));
      }
    };
    void run();
    return () => {
      const superseded = state.superseded;
      flags.live = false;
      if (!superseded) cancelCeremony();
    };
  }, [signedOut]);

  if (session.isPending) return <Loading />;
  if (session.data?.authenticated) return <Navigate to={next} replace />;

  const signIn = async () => {
    setBusy(true);
    setErr(undefined);
    const af = autofill.current;
    if (af) {
      af.superseded = true;
      await af.begun;
    }
    try {
      const options = await loginBegin();
      const credential = await getAssertion(options);
      await doneRef.current(await loginFinish(credential));
    } catch (e) {
      setErr(authError(e));
      setBusy(false);
    }
  };

  return (
    <section className="page auth-page">
      <h1>{t('login.title')}</h1>
      <p>{t('login.intro')}</p>
      <form
        className="auth-form"
        onSubmit={(e) => {
          e.preventDefault();
          void signIn();
        }}
      >
        <label htmlFor={inputId}>{t('login.handleLabel')}</label>
        <input id={inputId} name="username" type="text" autoComplete="username webauthn" />
        <button type="submit" className="primary" data-testid="login-passkey" disabled={busy}>
          {busy ? t('login.working') : t('login.button')}
        </button>
      </form>
      {err ? (
        <p className="auth-error" role="alert">
          {t(err.key as 'errors:unknown', err.params)}
        </p>
      ) : null}
      <div className="auth-join">
        <p className="muted">{t('login.noAccount')}</p>
        <Link to="/join" className="button" data-testid="login-join">
          {t('login.join')}
        </Link>
      </div>
    </section>
  );
}
