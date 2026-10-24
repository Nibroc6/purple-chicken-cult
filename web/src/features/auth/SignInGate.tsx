// The sign-in gate around the routed page (Shell). Signed out on a member page
// it shows a sign-in prompt instead of letting the page fail with uniform 404s
// that look like being offline. When the session ended during this visit
// (expired, or signed out on another device), the prompt says so, covers "/"
// too, and keeps the page mounted but hidden: signing in again right here
// brings it back as it was (a half-written post included). It also runs the
// session watch (app/session.ts). Owner: P0-W01 → P1-W07.
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router';
import { isMemberPath } from '../../app/memberPaths';
import { adoptSession, sessionEnded, useSession, useSessionWatch } from '../../app/session';
import { loginBegin, loginFinish } from './api';
import { authError, type ErrorMsg } from './vm';
import { getAssertion } from './webauthn';
import './auth.css';

export function SignInGate({ children }: { children: ReactNode }) {
  useSessionWatch();
  const session = useSession();
  const { pathname } = useLocation();
  const ended = sessionEnded(session.data);
  const gated = session.data?.authenticated === false && (isMemberPath(pathname) || (ended && pathname === '/'));
  return (
    <>
      {gated ? <SignInPrompt ended={ended} /> : null}
      {/* One wrapper either way, so gating never remounts the page. */}
      <div className="signin-gate-page" hidden={gated}>
        {gated && !ended ? null : children}
      </div>
    </>
  );
}

// SignInPrompt signs in with a passkey in place. It never starts passkey
// autofill: right after a sign-out that would race into signing back in.
export function SignInPrompt({ ended }: { ended: boolean }) {
  const { t } = useTranslation(['auth', 'errors']);
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<ErrorMsg | undefined>();
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    heading.current?.focus();
  }, []);

  const signIn = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      const options = await loginBegin();
      const credential = await getAssertion(options);
      adoptSession(qc, await loginFinish(credential));
    } catch (e) {
      setErr(authError(e));
      setBusy(false);
    }
  };

  return (
    <section className="page auth-page signin-prompt" data-testid="signin-prompt" data-ended={ended ? 'true' : 'false'}>
      <h1 ref={heading} tabIndex={-1}>
        {ended ? t('gate.endedTitle') : t('gate.title')}
      </h1>
      <p>{ended ? t('gate.endedBody') : t('gate.body')}</p>
      <div className="auth-actions">
        <button
          type="button"
          className="primary"
          data-testid="signin-prompt-button"
          disabled={busy}
          onClick={() => {
            void signIn();
          }}
        >
          {busy ? t('login.working') : t('login.button')}
        </button>
      </div>
      {err ? (
        <p className="auth-error" role="alert">
          {t(err.key as 'errors:unknown', err.params)}
        </p>
      ) : null}
      {ended ? null : <p className="muted">{t('login.noAccount')}</p>}
    </section>
  );
}
