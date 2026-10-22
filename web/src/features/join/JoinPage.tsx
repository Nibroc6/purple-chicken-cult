// The claiming device of an in-person handshake (§7, §10.8, plan P1-W02): the
// invitee (guest session) or, for a friend+ upgrade, the signed-in friend.
// Scan the initiator's rotating QR code with the camera (this device makes its
// own keys and claims the code), show the 6-digit code the initiator confirms,
// play the handshake sound (or, without sound, scan the second code), and then:
// an invitee accepts the terms, picks a username, creates a passkey and signs
// the Invite and Friendship records with it (the signup ceremony); a friend
// co-signs the friend+ record. Scan, Waiting, Sound and Dual are exported for
// the recovering device of a guardian recovery (features/recovery, steward:
// contract request p1-w10-1). Owner: P0-W04 → P1-W02.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router';
import { ApiError } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { sessionKey, useSession } from '../../app/session';
import { SecondPasskeyPrompt } from '../auth/SecondPasskeyPrompt';
import { createPasskey, getAssertion } from '../auth/webauthn';
import { RecordView, requestCosign, toRecordVMs } from '../cosign';
import { cosignHandshake } from '../invites/api';
import { handshakeAudio } from '../invites/audio';
import { ClaimDevice, isDualFrame, isFrame } from '../invites/handshake';
import { passed, type StatusVM } from '../invites/vm';
import {
  audioStart,
  claim,
  dualFrame,
  joinStart,
  joinStatus,
  registerBegin,
  registerCosign,
  registerFinish,
  type JoinStart,
  type Signup,
} from './api';
import { CameraError, startScanner } from './scanner';
import { browserLanguages, joinErrorKey } from './vm';
import './join.css';

type Step =
  | { kind: 'intro' }
  | { kind: 'scanning' }
  | { kind: 'claimed' }
  | { kind: 'sound'; st: StatusVM }
  | { kind: 'form' }
  | { kind: 'upgrade' }
  | { kind: 'ended'; reason: 'cancelled' | 'expired' }
  | { kind: 'done'; inviter: string | undefined }
  | { kind: 'friendPlus' }
  | { kind: 'recovered' };

export default function JoinPage() {
  const { t } = useTranslation(['join', 'errors']);
  const session = useSession();
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>({ kind: 'intro' });
  const [start, setStart] = useState<JoinStart | undefined>();
  const [err, setErr] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const [d, setDev] = useState<ClaimDevice | undefined>();
  const signedIn = session.data?.authenticated === true;

  const begin = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      setDev(await ClaimDevice.create());
      if (!signedIn) setStart(await joinStart());
      setStep({ kind: 'scanning' });
    } catch (e) {
      setErr(joinErrorKey(e));
    } finally {
      setBusy(false);
    }
  };

  // passedTo moves on once the handshake's outcome passed.
  const passedTo = (st: StatusVM) => {
    if (st.kind === 'upgrade') setStep({ kind: 'upgrade' });
    else if (st.kind === 'recovery') setStep({ kind: 'recovered' });
    else setStep({ kind: 'form' });
  };

  if (session.isPending) return <Loading />;
  return (
    <section className="page join-page" data-testid="join-page" data-step={step.kind}>
      <h1>{step.kind === 'done' ? t('done.title') : signedIn ? t('page.friendTitle') : t('page.title')}</h1>
      {step.kind === 'intro' ? (
        signedIn ? (
          <>
            <p>{t('signedIn')}</p>
            <p>{t('page.friendIntro')}</p>
            <button type="button" data-testid="join-scan-friend" disabled={busy} onClick={() => void begin()}>
              {t('scan.startFriend')}
            </button>
            <p>
              <Link to="/">{t('home')}</Link>
            </p>
          </>
        ) : (
          <>
            <p>{t('page.intro')}</p>
            <button type="button" data-testid="join-scan" disabled={busy} onClick={() => void begin()}>
              {t('scan.start')}
            </button>
          </>
        )
      ) : null}
      {step.kind === 'scanning' && d ? (
        <Scan
          accept={isFrame}
          onFrame={(p) => claim(d, p)}
          onDone={() => {
            setStep({ kind: 'claimed' });
          }}
        />
      ) : null}
      {step.kind === 'claimed' && d ? (
        <Waiting
          dev={d}
          onConfirmed={(st) => {
            if (passed(st) && !(st.kind === 'upgrade' && st.outcome !== 'verified')) passedTo(st);
            else setStep({ kind: 'sound', st });
          }}
          onEnded={(reason) => {
            setStep({ kind: 'ended', reason });
          }}
        />
      ) : null}
      {step.kind === 'sound' && d ? (
        step.st.mode === 'dual_qr' ? (
          <Dual dev={d} onPassed={passedTo} />
        ) : (
          <Sound dev={d} first={step.st} onPassed={passedTo} />
        )
      ) : null}
      {step.kind === 'form' && d && start ? (
        <Register
          dev={d}
          start={start}
          onJoined={(inviter) => {
            setStep({ kind: 'done', inviter });
          }}
        />
      ) : null}
      {step.kind === 'upgrade' && d ? (
        <UpgradeSign
          dev={d}
          onDone={() => {
            setStep({ kind: 'friendPlus' });
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
      {step.kind === 'friendPlus' ? (
        <>
          <p role="status" data-testid="join-friend-plus">
            {t('upgrade.done')}
          </p>
          <Link to="/friends">{t('upgrade.friends')}</Link>
        </>
      ) : null}
      {step.kind === 'recovered' ? (
        <p role="status" data-testid="join-recovered">
          {t('recovery.done')}
        </p>
      ) : null}
      {err ? (
        <p className="join-error" role="alert">
          {t(err as 'errors:unknown')}
        </p>
      ) : null}
    </section>
  );
}

// Scan runs the camera until onFrame accepted a decoded code. A code the
// server refused is not sent again; the next rotation is.
export function Scan({
  accept,
  onFrame,
  onDone,
  hint,
}: {
  accept: (payload: string) => boolean;
  onFrame: (payload: string) => Promise<unknown>;
  onDone: () => void;
  hint?: string;
}) {
  const { t } = useTranslation('join');
  const video = useRef<HTMLVideoElement>(null);
  const [problem, setProblem] = useState<'camera' | 'bad' | undefined>();
  const cb = useRef({ accept, onFrame, onDone });
  useEffect(() => {
    cb.current = { accept, onFrame, onDone };
  });

  useEffect(() => {
    let live = true;
    let stop: (() => void) | undefined;
    const tried = new Set<string>();
    let busy = false;
    const handle = (payload: string) => {
      if (busy || tried.has(payload) || !cb.current.accept(payload)) return;
      tried.add(payload);
      busy = true;
      cb.current.onFrame(payload).then(
        () => {
          if (!live) return;
          stop?.();
          cb.current.onDone();
        },
        () => {
          busy = false;
          if (live) setProblem('bad');
        },
      );
    };
    if (video.current) {
      startScanner(video.current, handle).then(
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
      <p>{hint ?? t('scan.hint')}</p>
      <video ref={video} className="join-video" aria-label={t('scan.videoLabel')} data-testid="join-video" />
      {problem ? (
        <p className="join-error" role="alert">
          {problem === 'camera' ? t('scan.noCamera') : t('scan.bad')}
        </p>
      ) : null}
    </div>
  );
}

// useJoinStatus polls the signed join status.
function useJoinStatus(dev: ClaimDevice, every = 1500) {
  return useQuery({ queryKey: ['join', 'status'], queryFn: () => joinStatus(dev), refetchInterval: every, gcTime: 0 });
}

// Waiting shows the SAS until the initiator confirms or cancels.
export function Waiting({
  dev,
  onConfirmed,
  onEnded,
}: {
  dev: ClaimDevice;
  onConfirmed: (st: StatusVM) => void;
  onEnded: (r: 'cancelled' | 'expired') => void;
}) {
  const { t } = useTranslation('join');
  const q = useJoinStatus(dev);
  const st = q.data;
  const cb = useRef({ onConfirmed, onEnded });
  useEffect(() => {
    cb.current = { onConfirmed, onEnded };
  });
  useEffect(() => {
    if (!st) return;
    if (st.state === 'confirmed') cb.current.onConfirmed(st);
    if (st.state === 'cancelled' || st.state === 'expired') cb.current.onEnded(st.state);
  }, [st]);
  return (
    <div className="join-sas">
      <h2>{t('sas.title')}</h2>
      {st?.sas ? (
        <p className="join-sas-code" data-testid="join-sas">
          {st.sas}
        </p>
      ) : (
        <Loading />
      )}
      <p>{st?.kind === 'upgrade' ? t('sas.bodyFriend') : t('sas.body')}</p>
      <p className="muted" role="status">
        {t('sas.waiting')}
      </p>
    </div>
  );
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

// Sound plays the handshake sound and waits for the initiator's report.
export function Sound({ dev, first, onPassed }: { dev: ClaimDevice; first: StatusVM; onPassed: (st: StatusVM) => void }) {
  const { t } = useTranslation(['join', 'errors']);
  const [st, setSt] = useState<StatusVM>(first);
  const [phase, setPhase] = useState<'ready' | 'playing' | 'waiting'>('ready');
  const [err, setErr] = useState<string | undefined>();
  const kind = st.kind;
  const play = async () => {
    setErr(undefined);
    try {
      const k = await dev.kmac();
      const audio = handshakeAudio(); // created on the click (autoplay rules)
      const attempt = await audioStart(dev, k);
      setPhase('playing');
      await audio.play(await dev.bits(k, attempt), { quiet: st.quiet });
      setPhase('waiting');
      for (let i = 0; i < 40; i++) {
        const now = await joinStatus(dev);
        if (now.attempt === attempt && !now.playing && now.outcome) {
          setSt(now);
          setPhase('ready');
          if (passed(now) && !(kind === 'upgrade' && now.outcome !== 'verified')) onPassed(now);
          return;
        }
        await sleep(500);
      }
      setPhase('ready');
    } catch (e) {
      setPhase('ready');
      setErr(e instanceof ApiError ? joinErrorKey(e) : 'join:sound.failed');
    }
  };
  const retry = st.outcome === 'retry';
  return (
    <div className="join-sound">
      <h2>{t('sound.title')}</h2>
      <p>{st.quiet ? t('sound.bodyQuiet') : t('sound.body')}</p>
      {retry ? (
        <p role="status" data-testid="join-outcome" data-outcome="retry">
          {st.hint === 'try_jingle'
            ? t('sound.retryJingle')
            : st.hint === 'volume_up'
              ? t('sound.retryVolume')
              : t('sound.retry')}
        </p>
      ) : null}
      {phase === 'ready' && st.attemptsLeft > 0 ? (
        <button type="button" data-testid="join-play" onClick={() => void play()}>
          {retry ? t('sound.again') : t('sound.play')}
        </button>
      ) : null}
      {phase === 'playing' ? (
        <p role="status" data-testid="join-playing">
          {t('sound.playing')}
        </p>
      ) : null}
      {phase === 'waiting' ? (
        <p role="status" className="muted">
          {t('sound.waiting')}
        </p>
      ) : null}
      {st.attemptsLeft === 0 && phase === 'ready' ? <p role="alert">{t('sound.noAttempts')}</p> : null}
      {err ? (
        <p className="join-error" role="alert">
          {t(err as 'errors:unknown')}
        </p>
      ) : null}
    </div>
  );
}

// Dual scans the initiator's second code (no sound; Accepted at most).
export function Dual({ dev, onPassed }: { dev: ClaimDevice; onPassed: (st: StatusVM) => void }) {
  const { t } = useTranslation('join');
  const [retry, setRetry] = useState(false);
  return (
    <div className="join-sound">
      <h2>{t('dual.title')}</h2>
      {retry ? (
        <p role="status" data-testid="join-outcome" data-outcome="retry">
          {t('dual.retry')}
        </p>
      ) : null}
      <Scan
        key={String(retry)}
        hint={t('dual.hint')}
        accept={isDualFrame}
        onFrame={async (p) => {
          const outcome = await dualFrame(dev, p, await dev.kmac());
          if (outcome === 'retry') {
            setRetry(true);
            throw new Error('retry');
          }
        }}
        onDone={() => {
          void joinStatus(dev).then(onPassed);
        }}
      />
    </div>
  );
}

function Register({ dev, start, onJoined }: { dev: ClaimDevice; start: JoinStart; onJoined: (inviter: string | undefined) => void }) {
  const { t, i18n } = useTranslation(['join', 'errors']);
  const qc = useQueryClient();
  const id = useId();
  const [handle, setHandle] = useState('');
  const [name, setName] = useState('');
  const [tos, setTos] = useState(false);
  const [age, setAge] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const [signup, setSignup] = useState<Signup | undefined>();
  const ready = handle.trim() !== '' && tos && age && !busy;

  const submit = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      const st = await joinStatus(dev);
      // A fresh ceremony per attempt (each is single use on the server).
      const options = await registerBegin(
        dev,
        {
          handle: handle.trim(),
          displayName: name.trim(),
          tosVersion: start.tosVersion,
          ageAttested: age,
          locale: i18n.language,
          languages: browserLanguages(navigator.languages),
        },
        st.signupToken ?? '',
      );
      const credential = await createPasskey(options);
      setSignup(await registerFinish(dev, credential));
    } catch (e) {
      setErr(joinErrorKey(e));
    } finally {
      setBusy(false);
    }
  };

  if (signup) {
    return (
      <SignupCeremony
        dev={dev}
        signup={signup}
        onJoined={(sv) => {
          qc.setQueryData(sessionKey, sv);
          onJoined(sv.me?.inviter.handle);
        }}
        onRestart={() => {
          setSignup(undefined);
        }}
      />
    );
  }
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
        {/* A new tab, so the half-filled form and the handshake stay put. */}
        <p className="join-terms-link">
          <a href="/terms" target="_blank" rel="noopener" data-testid="join-terms">
            {t('form.readTerms')}
          </a>
        </p>
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

// SignupCeremony shows the drafted Invite and Friendship records and signs
// them with the new passkey; the account exists only once that signature and
// the inviter's are in (fidelity #21).
function SignupCeremony({
  dev,
  signup,
  onJoined,
  onRestart,
}: {
  dev: ClaimDevice;
  signup: Signup;
  onJoined: (sv: Awaited<ReturnType<typeof registerCosign>>) => void;
  onRestart: () => void;
}) {
  const { t } = useTranslation(['join', 'errors']);
  const [waiting, setWaiting] = useState(false);
  const records = toRecordVMs(signup.records);
  const sign = useMutation({
    mutationFn: async () => {
      const assertion = await getAssertion(signup.options);
      for (;;) {
        try {
          return await registerCosign(dev, assertion);
        } catch (e) {
          if (!(e instanceof ApiError && e.code === 'awaiting_inviter')) throw e;
          setWaiting(true);
          // The same assertion goes again once the inviter signed.
          for (let i = 0; i < 600; i++) {
            await sleep(1000);
            if ((await joinStatus(dev)).peerSigned) break;
          }
        }
      }
    },
    onSuccess: onJoined,
    onSettled: () => {
      setWaiting(false);
    },
  });
  const errKey = sign.error ? joinErrorKey(sign.error) : undefined;
  return (
    <div className="join-ceremony" data-testid="join-ceremony">
      <h2>{t('ceremony.title')}</h2>
      <p>{t('ceremony.body')}</p>
      {records.map((r) => (
        <RecordView key={r.rawKind} record={r} />
      ))}
      <button type="button" data-testid="join-sign" disabled={sign.isPending}
        onClick={() => {
          sign.mutate();
        }}
      >
        {sign.isPending ? t('ceremony.working') : t('ceremony.sign')}
      </button>
      {waiting ? (
        <p role="status" data-testid="join-awaiting-inviter">
          {t('ceremony.awaiting')}
        </p>
      ) : null}
      {sign.error ? (
        <>
          {errKey ? (
            <p className="join-error" role="alert">
              {t(errKey as 'errors:unknown')}
            </p>
          ) : null}
          <button type="button" onClick={onRestart}>
            {t('ceremony.restart')}
          </button>
        </>
      ) : null}
    </div>
  );
}

// UpgradeSign co-signs the friend+ Friendship of a Verified upgrade.
function UpgradeSign({ dev, onDone }: { dev: ClaimDevice; onDone: () => void }) {
  const { t } = useTranslation(['join', 'errors']);
  const q = useJoinStatus(dev, 1000);
  const st = q.data;
  const asked = useRef(false);
  const sign = useMutation({
    mutationFn: async (hs: string) => {
      const r = await requestCosign({ purpose: 'friend_upgrade', target: hs, summary: t('upgrade.summary') });
      if (!r) return;
      await cosignHandshake(hs, r.intent, r.assertion);
      await q.refetch();
    },
  });
  const cb = useRef(onDone);
  useEffect(() => {
    cb.current = onDone;
  });
  useEffect(() => {
    if (st?.state === 'done') cb.current();
    if (st?.sign === 'needed' && st.hs && !asked.current) {
      asked.current = true;
      sign.mutate(st.hs);
    }
  }, [st, sign]);
  return (
    <div className="join-sound">
      <h2>{t('upgrade.title')}</h2>
      {st?.sign === 'waiting' ? (
        <p role="status">{t('upgrade.waiting')}</p>
      ) : st?.hs ? (
        <button
          type="button"
          data-testid="join-upgrade-sign"
          disabled={sign.isPending}
          onClick={() => {
            sign.mutate(st.hs ?? '');
          }}
        >
          {t('upgrade.sign')}
        </button>
      ) : (
        <Loading />
      )}
      {sign.error ? (
        <p className="join-error" role="alert">
          {t(joinErrorKey(sign.error) as 'errors:unknown')}
        </p>
      ) : null}
    </div>
  );
}
