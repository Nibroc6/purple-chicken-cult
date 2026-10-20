// Enrollment by link (§10.3, §10.8; security #13): genesis accounts and
// manual recovery. The one-time token lives in the URL fragment, which
// browsers never send; the page reads it, wipes it from the address bar and
// history, and POSTs it. Genesis records the terms version and the age
// attestation (D29); recovery lists the existing passkeys with a "remove old"
// option. Afterwards the optional second-passkey step. Owner: P0-W01 → P1-W07.
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { Loading } from '../../app/Loading';
import { sessionKey } from '../../app/session';
import { enrollBegin, enrollFinish, type EnrollStart } from './api';
import { settledSession } from './passkeyFlow';
import { SecondPasskeyPrompt } from './SecondPasskeyPrompt';
import { authErrorKey, readFragmentToken, toPasskeyVM } from './vm';
import { createPasskey } from './webauthn';
import './auth.css';

type State = { kind: 'checking' } | { kind: 'invalid' } | { kind: 'ready'; start: EnrollStart; token: string } | { kind: 'second' };

export default function EnrollPage() {
  const { t } = useTranslation(['auth', 'errors']);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [state, setState] = useState<State>({ kind: 'checking' });
  const token = useRef<string | undefined>(undefined);
  const begun = useRef<Promise<EnrollStart> | null>(null);

  useEffect(() => {
    if (!begun.current) {
      token.current = readFragmentToken(globalThis.location, globalThis.history);
      const tok = token.current;
      // The app's first session request must settle before the enrollment
      // session exists, or its cookie-less answer wipes that session's CSRF
      // token (passkeyFlow.ts settledSession).
      begun.current = tok ? settledSession(qc).then(() => enrollBegin(tok)) : Promise.reject(new Error('no token'));
    }
    let live = true;
    begun.current.then(
      (start) => {
        if (live) setState({ kind: 'ready', start, token: token.current ?? '' });
      },
      () => {
        if (live) setState({ kind: 'invalid' });
      },
    );
    return () => {
      live = false;
    };
  }, [qc]);

  if (state.kind === 'checking') return <Loading />;
  return (
    <section className="page auth-page">
      <h1>{t('enroll.title')}</h1>
      {state.kind === 'invalid' ? <p role="alert">{t('enroll.invalid')}</p> : null}
      {state.kind === 'ready' ? (
        <EnrollForm
          start={state.start}
          token={state.token}
          onEnrolled={() => {
            setState({ kind: 'second' });
          }}
          onSession={(sv) => {
            qc.setQueryData(sessionKey, sv);
          }}
        />
      ) : null}
      {state.kind === 'second' ? (
        <SecondPasskeyPrompt
          onDone={() => {
            void navigate('/', { replace: true });
          }}
        />
      ) : null}
    </section>
  );
}

function EnrollForm({
  start,
  token,
  onEnrolled,
  onSession,
}: {
  start: EnrollStart;
  token: string;
  onEnrolled: () => void;
  onSession: (sv: Awaited<ReturnType<typeof enrollFinish>>) => void;
}) {
  const { t } = useTranslation(['auth', 'errors']);
  const genesis = start.purpose === 'genesis';
  const [tos, setTos] = useState(false);
  const [age, setAge] = useState(false);
  const [removeOld, setRemoveOld] = useState(true);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const existing = start.existing.map(toPasskeyVM);
  const ready = !genesis || (tos && age);

  const create = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      // A fresh ceremony per attempt (each is single use on the server).
      const { options } = await enrollBegin(token);
      const credential = await createPasskey(options);
      const sv = await enrollFinish(
        genesis ? { credential, tosVersion: start.tosVersion, ageAttested: age } : { credential, removeOld },
      );
      onSession(sv);
      onEnrolled();
    } catch (e) {
      setErr(authErrorKey(e));
      setBusy(false);
    }
  };

  return (
    <form
      className="auth-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready) void create();
      }}
    >
      <p>{genesis ? t('enroll.genesisIntro') : t('enroll.recoveryIntro')}</p>
      {genesis ? (
        <fieldset>
          <legend>{t('enroll.terms')}</legend>
          <label className="auth-choice">
            <input
              type="checkbox"
              name="tos"
              checked={tos}
              onChange={(e) => {
                setTos(e.target.checked);
              }}
            />
            {t('enroll.tos', { version: start.tosVersion })}
          </label>
          <label className="auth-choice">
            <input
              type="checkbox"
              name="age"
              checked={age}
              onChange={(e) => {
                setAge(e.target.checked);
              }}
            />
            {t('enroll.age', { age: start.minAge })}
          </label>
        </fieldset>
      ) : (
        <fieldset>
          <legend>{t('enroll.existing')}</legend>
          <ul className="auth-list">
            {existing.map((p) => (
              <li key={p.id}>
                {p.name || t('passkeys.unnamed')}{' '}
                <span className="muted">{t('passkeys.created', { date: p.createdAt })}</span>
              </li>
            ))}
          </ul>
          <label className="auth-choice">
            <input
              type="checkbox"
              name="remove_old"
              checked={removeOld}
              onChange={(e) => {
                setRemoveOld(e.target.checked);
              }}
            />
            {t('enroll.removeOld')}
          </label>
        </fieldset>
      )}
      {err ? (
        <p className="auth-error" role="alert">
          {t(err as 'errors:unknown')}
        </p>
      ) : null}
      <div className="auth-actions">
        <button type="submit" className="primary" data-testid="enroll-create" disabled={busy || !ready}>
          {busy ? t('enroll.working') : t('enroll.button')}
        </button>
      </div>
    </form>
  );
}
