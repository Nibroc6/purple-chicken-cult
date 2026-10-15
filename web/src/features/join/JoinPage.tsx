// The invitee side of an in-person invite (§7, §10.8, plan §4.7 invites):
// scan the inviter's rotating QR code with the camera, show the 6-digit code
// the inviter confirms ("codes match"), then the terms and age attestation,
// the username and the passkey, and finally the optional second passkey.
// Owner: P0-W04 → P1-W02.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router';
import { Loading } from '../../app/Loading';
import { sessionKey, useSession } from '../../app/session';
import { SecondPasskeyPrompt } from '../auth/SecondPasskeyPrompt';
import { createPasskey } from '../auth/webauthn';
import { claim, joinStart, joinStatus, registerBegin, registerFinish, type JoinStart } from './api';
import { CameraError, startScanner } from './scanner';
import { browserLanguages, joinErrorKey } from './vm';
import './join.css';

type Step =
  | { kind: 'intro' }
  | { kind: 'scanning'; start: JoinStart }
  | { kind: 'claimed'; start: JoinStart }
  | { kind: 'form'; start: JoinStart }
  | { kind: 'ended'; reason: 'cancelled' | 'expired' }
  | { kind: 'done'; inviter: string | undefined };

export default function JoinPage() {
  const { t } = useTranslation(['join', 'errors']);
  const session = useSession();
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>({ kind: 'intro' });
  const [err, setErr] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);

  const begin = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      setStep({ kind: 'scanning', start: await joinStart() });
    } catch (e) {
      setErr(joinErrorKey(e));
    } finally {
      setBusy(false);
    }
  };

  if (session.isPending) return <Loading />;
  if (step.kind === 'intro' && session.data?.authenticated) {
    return (
      <section className="page join-page">
        <h1>{t('page.title')}</h1>
        <p>{t('signedIn')}</p>
        <Link to="/">{t('home')}</Link>
      </section>
    );
  }
  return (
    <section className="page join-page" data-testid="join-page" data-step={step.kind}>
      <h1>{step.kind === 'done' ? t('done.title') : t('page.title')}</h1>
      {step.kind === 'intro' ? (
        <>
          <p>{t('page.intro')}</p>
          <button type="button" data-testid="join-scan" disabled={busy} onClick={() => void begin()}>
            {t('scan.start')}
          </button>
        </>
      ) : null}
      {step.kind === 'scanning' ? (
        <Scan
          onClaimed={() => {
            setStep({ kind: 'claimed', start: step.start });
          }}
        />
      ) : null}
      {step.kind === 'claimed' ? (
        <Waiting
          onConfirmed={() => {
            setStep({ kind: 'form', start: step.start });
          }}
          onEnded={(reason) => {
            setStep({ kind: 'ended', reason });
          }}
        />
      ) : null}
      {step.kind === 'form' ? (
        <Register
          start={step.start}
          onJoined={(inviter) => {
            setStep({ kind: 'done', inviter });
          }}
        />
      ) : null}
      {step.kind === 'ended' ? (
        <>
          <p role="status" data-testid="join-ended">
            {step.reason === 'cancelled' ? t('ended.cancelled') : t('ended.expired')}
          </p>
          <button type="button" disabled={busy} onClick={() => void begin()}>
            {t('ended.again')}
          </button>
        </>
      ) : null}
      {step.kind === 'done' ? (
        <>
          {step.inviter ? (
            <p data-testid="join-invited-by">{t('done.invitedBy', { handle: `@${step.inviter}` })}</p>
          ) : null}
          <SecondPasskeyPrompt
            onDone={() => {
              void navigate('/', { replace: true });
            }}
          />
        </>
      ) : null}
      {err ? (
        <p className="join-error" role="alert">
          {t(err as 'errors:unknown')}
        </p>
      ) : null}
    </section>
  );
}

// Scan runs the camera until a frame is claimed. A frame the server refused is
// not sent again; the next rotation is.
function Scan({ onClaimed }: { onClaimed: () => void }) {
  const { t } = useTranslation('join');
  const video = useRef<HTMLVideoElement>(null);
  const [problem, setProblem] = useState<'camera' | 'bad' | undefined>();
  const done = useRef(onClaimed);
  useEffect(() => {
    done.current = onClaimed;
  });

  useEffect(() => {
    let live = true;
    let stop: (() => void) | undefined;
    const tried = new Set<string>();
    let claiming = false;
    const onFrame = (payload: string) => {
      if (claiming || tried.has(payload)) return;
      tried.add(payload);
      claiming = true;
      claim(payload).then(
        () => {
          if (!live) return;
          stop?.();
          done.current();
        },
        () => {
          claiming = false;
          if (live) setProblem('bad');
        },
      );
    };
    if (video.current) {
      startScanner(video.current, onFrame).then(
        (s) => {
          if (live) stop = s.stop;
          else s.stop();
        },
        (e: unknown) => {
          if (live) setProblem(e instanceof CameraError ? 'camera' : 'bad');
        },
      );
    }
    return () => {
      live = false;
      stop?.();
    };
  }, []);

  return (
    <div className="join-scan">
      <p>{t('scan.hint')}</p>
      <video ref={video} className="join-video" aria-label={t('scan.videoLabel')} data-testid="join-video" />
      {problem ? (
        <p className="join-error" role="alert">
          {problem === 'camera' ? t('scan.noCamera') : t('scan.bad')}
        </p>
      ) : null}
    </div>
  );
}

// Waiting shows the SAS until the inviter confirms or cancels.
function Waiting({ onConfirmed, onEnded }: { onConfirmed: () => void; onEnded: (r: 'cancelled' | 'expired') => void }) {
  const { t } = useTranslation('join');
  const q = useQuery({
    queryKey: ['join', 'status'],
    queryFn: joinStatus,
    refetchInterval: 1500,
    gcTime: 0,
  });
  const state = q.data?.state;
  useEffect(() => {
    if (state === 'confirmed') onConfirmed();
    if (state === 'cancelled' || state === 'expired') onEnded(state);
  }, [state, onConfirmed, onEnded]);
  return (
    <div className="join-sas">
      <h2>{t('sas.title')}</h2>
      {q.data?.sas ? (
        <p className="join-sas-code" data-testid="join-sas">
          {q.data.sas}
        </p>
      ) : (
        <Loading />
      )}
      <p>{t('sas.body')}</p>
      <p className="muted" role="status">
        {t('sas.waiting')}
      </p>
    </div>
  );
}

function Register({ start, onJoined }: { start: JoinStart; onJoined: (inviter: string | undefined) => void }) {
  const { t, i18n } = useTranslation(['join', 'errors']);
  const qc = useQueryClient();
  const id = useId();
  const [handle, setHandle] = useState('');
  const [name, setName] = useState('');
  const [tos, setTos] = useState(false);
  const [age, setAge] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const ready = handle.trim() !== '' && tos && age && !busy;

  const submit = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      // A fresh ceremony per attempt (each is single use on the server).
      const options = await registerBegin({
        handle: handle.trim(),
        displayName: name.trim(),
        tosVersion: start.tosVersion,
        ageAttested: age,
        locale: i18n.language,
        languages: browserLanguages(navigator.languages),
      });
      const credential = await createPasskey(options);
      const sv = await registerFinish(credential);
      qc.setQueryData(sessionKey, sv);
      onJoined(sv.me?.inviter.handle);
    } catch (e) {
      setErr(joinErrorKey(e));
      setBusy(false);
    }
  };

  return (
    <form
      className="join-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready) void submit();
      }}
    >
      <h2>{t('form.title')}</h2>
      <div className="join-field">
        <label htmlFor={`${id}-handle`}>{t('form.handle')}</label>
        <input
          id={`${id}-handle`}
          name="handle"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={21}
          aria-describedby={`${id}-handle-hint`}
          value={handle}
          onChange={(e) => {
            setHandle(e.target.value);
          }}
        />
        <span id={`${id}-handle-hint`} className="muted">
          {t('form.handleHint')}
        </span>
      </div>
      <div className="join-field">
        <label htmlFor={`${id}-name`}>{t('form.name')}</label>
        <input
          id={`${id}-name`}
          name="display_name"
          autoComplete="name"
          maxLength={50}
          value={name}
          onChange={(e) => {
            setName(e.target.value);
          }}
        />
      </div>
      <fieldset>
        <legend>{t('form.terms')}</legend>
        <label className="join-choice">
          <input
            type="checkbox"
            name="tos"
            checked={tos}
            onChange={(e) => {
              setTos(e.target.checked);
            }}
          />
          {t('form.tos', { version: start.tosVersion })}
        </label>
        <label className="join-choice">
          <input
            type="checkbox"
            name="age"
            checked={age}
            onChange={(e) => {
              setAge(e.target.checked);
            }}
          />
          {t('form.age', { age: start.minAge })}
        </label>
      </fieldset>
      <button type="submit" data-testid="join-submit" disabled={!ready}>
        {busy ? t('form.working') : t('form.submit')}
      </button>
      {err ? (
        <p className="join-error" role="alert">
          {t(err as 'errors:unknown')}
        </p>
      ) : null}
    </form>
  );
}
