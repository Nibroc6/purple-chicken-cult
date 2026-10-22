// The recovering device of a guardian recovery (§10.3 Phase 1, D10): someone
// who lost every passkey asks a friend+ friend to open a recovery for them in
// person (Settings → Recovery). This device scans the friend's code (a guest
// session; the claim answers a ticket kept in this browser), shows the 6-digit
// code the friend confirms and plays the handshake sound. Then it follows the
// recovery: a second friend+ friend approves, the account's own devices can
// cancel during a 24 h hold, and once released this device creates the new
// passkey at the one-time enrollment link. Owner: P1-W10.
import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError, errorMsg, type ErrorMsg, errorText } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { formatDateTime } from '../../lib/time';
import { ClaimDevice, isFrame } from '../invites/handshake';
import { passed, type StatusVM } from '../invites/vm';
import { joinStart } from '../join/api';
import { Dual, Scan, Sound, Waiting } from '../join/JoinPage';
import { claimRecovery, fetchRecoveryStatus, recoveryErrorKey } from './api';
import { clearTicket, loadTicket, pollEvery, saveTicket } from './vm';
import './recovery.css';

type Step =
  | { kind: 'intro' }
  | { kind: 'scanning' }
  | { kind: 'claimed' }
  | { kind: 'sound'; st: StatusVM }
  | { kind: 'ended'; reason: 'cancelled' | 'expired' }
  | { kind: 'follow'; ticket: string };

export default function RecoverPage() {
  const { t } = useTranslation(['recovery', 'errors']);
  const [step, setStep] = useState<Step>(() => {
    const ticket = loadTicket();
    return ticket ? { kind: 'follow', ticket } : { kind: 'intro' };
  });
  const [dev, setDev] = useState<ClaimDevice | undefined>();
  const [ticket, setTicket] = useState<string | undefined>();
  const [err, setErr] = useState<ErrorMsg | undefined>();
  const [busy, setBusy] = useState(false);

  const begin = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      setDev(await ClaimDevice.create());
      await joinStart(); // the guest session the claim runs in
      setStep({ kind: 'scanning' });
    } catch (e) {
      setErr(errorMsg(e, recoveryErrorKey(e)));
    } finally {
      setBusy(false);
    }
  };
  const follow = () => {
    if (ticket) setStep({ kind: 'follow', ticket });
  };
  const restart = () => {
    clearTicket();
    setTicket(undefined);
    setStep({ kind: 'intro' });
  };

  return (
    <section className="page recovery-page" data-testid="recover-page" data-step={step.kind}>
      <h1>{t('recover.title')}</h1>
      {step.kind === 'intro' ? (
        <>
          <p>{t('recover.intro')}</p>
          <ol className="recovery-steps">
            <li>{t('recover.step1')}</li>
            <li>{t('recover.step2')}</li>
            <li>{t('recover.step3')}</li>
          </ol>
          <button type="button" data-testid="recover-scan" disabled={busy} onClick={() => void begin()}>
            {t('recover.scan')}
          </button>
        </>
      ) : null}
      {step.kind === 'scanning' && dev ? (
        <Scan
          hint={t('recover.scanHint')}
          accept={isFrame}
          onFrame={async (p) => {
            const tk = await claimRecovery(dev, p);
            saveTicket(tk);
            setTicket(tk);
          }}
          onDone={() => {
            setStep({ kind: 'claimed' });
          }}
        />
      ) : null}
      {step.kind === 'claimed' && dev ? (
        <Waiting
          dev={dev}
          onConfirmed={(st) => {
            if (passed(st)) follow();
            else setStep({ kind: 'sound', st });
          }}
          onEnded={(reason) => {
            setStep({ kind: 'ended', reason });
          }}
        />
      ) : null}
      {step.kind === 'sound' && dev ? (
        step.st.mode === 'dual_qr' ? (
          <Dual dev={dev} onPassed={follow} />
        ) : (
          <Sound dev={dev} first={step.st} onPassed={follow} />
        )
      ) : null}
      {step.kind === 'ended' ? (
        <>
          <p role="status" data-testid="recover-ended">
            {step.reason === 'cancelled' ? t('recover.handshakeCancelled') : t('recover.handshakeExpired')}
          </p>
          <button type="button" onClick={restart}>
            {t('recover.again')}
          </button>
        </>
      ) : null}
      {step.kind === 'follow' ? <Follow ticket={step.ticket} onRestart={restart} /> : null}
      {err ? (
        <p className="recovery-error" role="alert">
          {errorText(t, err)}
        </p>
      ) : null}
    </section>
  );
}

// Follow shows where the recovery stands, by ticket.
function Follow({ ticket, onRestart }: { ticket: string; onRestart: () => void }) {
  const { t, i18n } = useTranslation(['recovery', 'errors']);
  const q = useQuery({
    queryKey: ['recovery', 'status', ticket],
    queryFn: () => fetchRecoveryStatus(ticket),
    refetchInterval: (query) => pollEvery(query.state.data?.state),
    refetchOnWindowFocus: (query) => pollEvery(query.state.data?.state) !== false,
    retry: (n, e) => !(e instanceof ApiError && e.status === 404) && n < 3,
    gcTime: 0,
  });
  if (q.error instanceof ApiError && q.error.status === 404) {
    return (
      <>
        <p role="status" data-testid="recover-state" data-state="unknown">
          {t('recover.unknown')}
        </p>
        <button type="button" onClick={onRestart}>
          {t('recover.again')}
        </button>
      </>
    );
  }
  const st = q.data;
  if (!st) return q.error ? <p role="alert">{errorText(t, q.error, recoveryErrorKey)}</p> : <Loading />;
  const at = st.releaseAt ? formatDateTime(st.releaseAt, i18n.language) : '';
  return (
    <div className="recovery-follow" data-testid="recover-state" data-state={st.state}>
      {st.state === 'claimed' ? <p role="status">{t('recover.state.claimed')}</p> : null}
      {st.state === 'approving' ? (
        <p role="status">{t('recover.state.approving', { approvals: st.approvals ?? 1, needed: st.needed ?? 2 })}</p>
      ) : null}
      {st.state === 'holding' ? <p role="status">{t('recover.state.holding', { at })}</p> : null}
      {st.state === 'released' && st.enrollURL ? (
        <>
          <p role="status">{t('recover.state.released')}</p>
          <a href={st.enrollURL} data-testid="recover-enroll" onClick={clearTicket}>
            {t('recover.enroll')}
          </a>
        </>
      ) : null}
      {st.state === 'cancelled' || st.state === 'expired' ? (
        <>
          <p role="status">{st.state === 'cancelled' ? t('recover.state.cancelled') : t('recover.state.expired')}</p>
          <button type="button" onClick={onRestart}>
            {t('recover.again')}
          </button>
        </>
      ) : null}
    </div>
  );
}
