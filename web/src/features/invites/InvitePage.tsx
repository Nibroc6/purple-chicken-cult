// The invite screen (§7, §8, plan §4.7 invites): the saved invites with a
// countdown to the next one, or during probation a countdown to the end of its
// time part plus the outside friends still needed; then the rotating QR code,
// the SAS check ("codes match / don't match") and the outcome. Owner: P0-W04 → P1-W02.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import QRCode from 'qrcode';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { formatDateTime } from '../../lib/time';
import {
  cancelHandshake,
  confirmHandshake,
  fetchBudget,
  fetchFrame,
  fetchStatus,
  openHandshake,
  type OpenedVM,
} from './api';
import { ErrorText, invitesKey, useNow } from './shared';
import { canOpen, countdown, joinURL, type BudgetVM, type HandshakeState } from './vm';

const live: ReadonlySet<HandshakeState> = new Set(['open', 'claimed', 'confirmed']);

export default function InvitePage() {
  const { t } = useTranslation('invites');
  const qc = useQueryClient();
  const budget = useQuery({ queryKey: [...invitesKey, 'budget'], queryFn: fetchBudget });
  const [opened, setOpened] = useState<OpenedVM | undefined>();
  const open = useMutation({
    mutationFn: openHandshake,
    onSuccess: (o) => {
      setOpened(o);
    },
  });
  return (
    <section className="page invites-page">
      <h1>{t('page.title')}</h1>
      <p>{t('page.intro')}</p>
      {budget.isPending ? <Loading /> : budget.error ? <ErrorText err={budget.error} /> : <Budget b={budget.data} />}
      {opened ? (
        <Handshake
          key={opened.hs}
          opened={opened}
          onDone={() => {
            void qc.invalidateQueries({ queryKey: invitesKey });
          }}
          onAgain={() => {
            setOpened(undefined);
            open.mutate();
          }}
        />
      ) : (
        <div className="invites-actions">
          <button
            type="button"
            data-testid="invite-open"
            disabled={!budget.data || !canOpen(budget.data) || open.isPending}
            onClick={() => {
              open.mutate();
            }}
          >
            {open.isPending ? t('open.working') : t('open.button')}
          </button>
        </div>
      )}
      <ErrorText err={open.error} />
      <p>
        <Link to="/invite/people">{t('open.people')}</Link>
      </p>
    </section>
  );
}

function Budget({ b }: { b: BudgetVM }) {
  const { t, i18n } = useTranslation('invites');
  const now = useNow();
  const remaining = (target: Date) => {
    const c = countdown(target, now);
    return t(c.key, c.values);
  };
  if (b.revoked) {
    return (
      <div className="invites-budget" data-testid="invite-budget">
        <p>{t('budget.revoked')}</p>
      </div>
    );
  }
  if (b.pausedUntil) {
    return (
      <div className="invites-budget" data-testid="invite-budget">
        <p>{t('budget.paused', { date: formatDateTime(b.pausedUntil, i18n.language) })}</p>
      </div>
    );
  }
  if (b.probation) {
    const timeLeft = b.probation.until.getTime() > now.getTime();
    return (
      <div className="invites-budget" data-testid="invite-budget">
        <p>{t('budget.probationIntro')}</p>
        <p data-testid="invite-probation-time">
          {timeLeft ? t('budget.probationTime', { time: remaining(b.probation.until) }) : t('budget.probationTimeDone')}
        </p>
        {b.probation.needOutsideFriends > 0 ? (
          <p data-testid="invite-probation-friends">
            {t('budget.probationFriends', { count: b.probation.needOutsideFriends })}
          </p>
        ) : null}
      </div>
    );
  }
  return (
    <div className="invites-budget" data-testid="invite-budget">
      <p data-testid="invite-available">{t('budget.available', { count: b.available, cap: b.cap })}</p>
      {b.nextAt ? (
        <p data-testid="invite-next">
          {t('budget.next', { time: remaining(b.nextAt) })}
        </p>
      ) : (
        <p className="muted">{t('budget.full')}</p>
      )}
    </div>
  );
}

function Handshake({ opened, onDone, onAgain }: { opened: OpenedVM; onDone: () => void; onAgain: () => void }) {
  const { t } = useTranslation('invites');
  const status = useQuery({
    queryKey: [...invitesKey, 'hs', opened.hs],
    queryFn: () => fetchStatus(opened.hs),
    refetchInterval: (q) => (q.state.data && !live.has(q.state.data.state) ? false : 1000),
  });
  const state = status.data?.state ?? 'open';
  const doneRef = useRef(false);
  useEffect(() => {
    if (state === 'done' && !doneRef.current) {
      doneRef.current = true;
      onDone();
    }
  }, [state, onDone]);
  const confirm = useMutation({ mutationFn: () => confirmHandshake(opened.hs), onSuccess: () => status.refetch() });
  const cancel = useMutation({ mutationFn: () => cancelHandshake(opened.hs), onSuccess: () => status.refetch() });

  return (
    <div className="invites-handshake" data-testid="invite-handshake" data-state={state}>
      {state === 'open' ? <Frame opened={opened} /> : null}
      {state === 'claimed' && status.data?.sas ? (
        <>
          <p>{t('hs.claimed')}</p>
          <p className="invites-sas" data-testid="invite-sas">
            {status.data.sas}
          </p>
          <div className="invites-actions">
            <button
              type="button"
              data-testid="invite-match"
              disabled={confirm.isPending || cancel.isPending}
              onClick={() => {
                confirm.mutate();
              }}
            >
              {t('hs.match')}
            </button>
            <button
              type="button"
              data-testid="invite-mismatch"
              disabled={confirm.isPending || cancel.isPending}
              onClick={() => {
                cancel.mutate();
              }}
            >
              {t('hs.mismatch')}
            </button>
          </div>
        </>
      ) : null}
      {state === 'confirmed' ? <p role="status">{t('hs.confirmed')}</p> : null}
      {state === 'done' ? (
        <p role="status" data-testid="invite-done">
          {t('hs.done')}
        </p>
      ) : null}
      {state === 'cancelled' || state === 'expired' ? (
        <>
          <p role="status">{state === 'cancelled' ? t('hs.cancelled') : t('hs.expired')}</p>
          <button type="button" data-testid="invite-again" onClick={onAgain}>
            {t('hs.again')}
          </button>
        </>
      ) : null}
      {state === 'open' || state === 'confirmed' ? (
        <button
          type="button"
          disabled={cancel.isPending}
          onClick={() => {
            cancel.mutate();
          }}
        >
          {t('hs.cancel')}
        </button>
      ) : null}
      <ErrorText err={confirm.error ?? cancel.error} />
    </div>
  );
}

// Frame shows the rotating QR code; it fetches the next frame when the current
// one's window ends.
function Frame({ opened }: { opened: OpenedVM }) {
  const { t } = useTranslation('invites');
  const canvas = useRef<HTMLCanvasElement>(null);
  const frame = useQuery({
    queryKey: [...invitesKey, 'frame', opened.hs],
    queryFn: () => fetchFrame(opened.hs),
    refetchInterval: (q) =>
      q.state.data ? Math.max(500, q.state.data.validUntil.getTime() - Date.now() + 100) : opened.frameTTLms,
  });
  const payload = frame.data?.payload;
  useEffect(() => {
    if (!payload || !canvas.current) return;
    QRCode.toCanvas(canvas.current, payload, { errorCorrectionLevel: 'M', margin: 2, width: 320 }).catch(() => undefined);
  }, [payload]);
  return (
    <>
      <canvas
        ref={canvas}
        className="invites-qr"
        role="img"
        aria-label={t('hs.qrLabel')}
        data-testid="invite-qr"
      />
      <p>{t('hs.scanHint', { url: joinURL(location.origin) })}</p>
      <ErrorText err={frame.error} />
    </>
  );
}
