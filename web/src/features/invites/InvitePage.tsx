// The invite screen (§7, §8, plan §4.7 invites): the saved invites with a
// countdown to the next one, or during probation a countdown to the end of its
// time part plus the outside friends still needed; then the handshake (the
// rotating QR code, the SAS check, the sound or the dual QR, the co-signature)
// in HandshakeHost. One code, two uses: someone new signs up with it (while
// the budget allows), someone signed in becomes a friend (always).
// Owner: P0-W04 → P1-W02.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { formatDateTime } from '../../lib/time';
import { fetchBudget, openHandshake, type HandshakeMode, type OpenedVM } from './api';
import { HandshakeHost } from './HandshakeHost';
import { ErrorText, invitesKey, useNow } from './shared';
import { canInvite, canShowCode, countdown, type BudgetVM } from './vm';

export default function InvitePage() {
  const { t } = useTranslation('invites');
  const qc = useQueryClient();
  const budget = useQuery({ queryKey: [...invitesKey, 'budget'], queryFn: fetchBudget });
  const [opened, setOpened] = useState<OpenedVM | undefined>();
  const [mode, setMode] = useState<HandshakeMode>('qr_audio');
  const [quiet, setQuiet] = useState(false);
  const open = useMutation({
    mutationFn: () => openHandshake(mode, quiet),
    onSuccess: (o) => {
      setOpened(o);
    },
    // Opening uses an attempt (or was refused for want of one): show the count.
    onSettled: () => qc.invalidateQueries({ queryKey: [...invitesKey, 'budget'] }),
  });
  return (
    <section className="page invites-page">
      <h1>{t('page.title')}</h1>
      <p>{t('page.intro')}</p>
      {budget.isPending ? <Loading /> : budget.error ? <ErrorText err={budget.error} /> : <Budget b={budget.data} />}
      {opened && !opened.invite ? (
        <p className="muted" data-testid="invite-connect-only">
          {t('open.connectOnly')}
        </p>
      ) : null}
      {opened ? (
        <HandshakeHost
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
        <>
          <fieldset className="invites-mode">
            <legend>{t('mode.legend')}</legend>
            <label className="invites-choice">
              <input
                type="radio"
                name="mode"
                checked={mode === 'qr_audio'}
                onChange={() => {
                  setMode('qr_audio');
                }}
              />
              {t('mode.audio')}
            </label>
            {mode === 'qr_audio' ? (
              <label className="invites-choice invites-sub">
                <input
                  type="checkbox"
                  name="quiet"
                  data-testid="invite-quiet"
                  checked={quiet}
                  onChange={(e) => {
                    setQuiet(e.target.checked);
                  }}
                />
                {t('mode.quiet')}
              </label>
            ) : null}
            <label className="invites-choice">
              <input
                type="radio"
                name="mode"
                data-testid="invite-mode-dual"
                checked={mode === 'dual_qr'}
                onChange={() => {
                  setMode('dual_qr');
                }}
              />
              {t('mode.dual')}
            </label>
          </fieldset>
          <div className="invites-actions">
            <button
              type="button"
              data-testid="invite-open"
              disabled={!budget.data || !canShowCode(budget.data) || open.isPending}
              onClick={() => {
                open.mutate();
              }}
            >
              {open.isPending ? t('open.working') : t('open.button')}
            </button>
          </div>
          {budget.data && !canInvite(budget.data) ? <p className="muted">{t('open.connectOnly')}</p> : null}
        </>
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
            {b.probation.friendPlus
              ? t('budget.probationFriendsPlus', { count: b.probation.needOutsideFriends })
              : t('budget.probationFriends', { count: b.probation.needOutsideFriends })}
          </p>
        ) : null}
        <Attempts b={b} remaining={remaining} />
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
      <Attempts b={b} remaining={remaining} />
    </div>
  );
}

// Attempts shows the code attempts (they also limit codes that only connect).
function Attempts({ b, remaining }: { b: BudgetVM; remaining: (target: Date) => string }) {
  const { t } = useTranslation('invites');
  if (!b.attempts) return null;
  return (
    <div className="invites-attempts" data-testid="invite-attempts">
      <p data-testid="invite-attempts-left">{t('attempts.left', { count: b.attempts.available, cap: b.attempts.cap })}</p>
      {b.attempts.nextAt ? (
        <p data-testid="invite-attempts-next">{t('attempts.next', { time: remaining(b.attempts.nextAt) })}</p>
      ) : null}
      <p className="muted">{t('attempts.hint')}</p>
    </div>
  );
}
