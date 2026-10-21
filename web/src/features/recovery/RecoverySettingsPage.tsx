// Recovery settings: the guardian's side of a guardian recovery (§10.3 Phase
// 1). A friend+ friend who lost every passkey stands next to you: start their
// recovery here and let their device scan your code (the in-person handshake).
// A second friend+ friend of theirs then approves it here, signing with a
// passkey; their account's own devices can cancel during the 24 h hold.
// Owner: P1-W10.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatDateTime } from '../../lib/time';
import { requestCosign } from '../cosign';
import { HandshakeHost, type OpenedVM } from '../invites';
import { sessionCosigns } from '../settings/api';
import { SettingsShell } from '../settings/shared';
import { approveRecovery, fetchFriendPlus, fetchGuardianRequests, recoveryErrorKey, startRecovery, type FriendVM } from './api';
import type { GuardianRequestVM } from './vm';
import './recovery.css';

const requestsKey = ['recovery', 'requests'] as const;
const friendsKey = ['recovery', 'friendPlus'] as const;
const cosignsKey = ['recovery', 'cosigns'] as const;

function ErrorLine({ err }: { err: unknown }) {
  const { t } = useTranslation(['recovery', 'errors']);
  if (!err) return null;
  return (
    <p className="recovery-error" role="alert">
      {t(recoveryErrorKey(err) as 'errors:unknown')}
    </p>
  );
}

export default function RecoverySettingsPage() {
  const { t } = useTranslation('recovery');
  return (
    <SettingsShell title={t('settings.title')}>
      {() => (
        <>
          <Approvals />
          <HelpFriend />
        </>
      )}
    </SettingsShell>
  );
}

// Approvals lists the recoveries waiting for the viewer as second guardian.
function Approvals() {
  const { t } = useTranslation('recovery');
  const q = useQuery({ queryKey: requestsKey, queryFn: fetchGuardianRequests, refetchInterval: 30_000 });
  return (
    <section className="recovery-section" data-testid="recovery-approvals">
      <h2>{t('approve.title')}</h2>
      {q.data && q.data.length === 0 ? <p className="muted">{t('approve.none')}</p> : null}
      <ul className="recovery-list">
        {q.data?.map((r) => (
          <Approval key={r.id} r={r} />
        ))}
      </ul>
      <ErrorLine err={q.error} />
    </section>
  );
}

function Approval({ r }: { r: GuardianRequestVM }) {
  const { t, i18n } = useTranslation('recovery');
  const qc = useQueryClient();
  const cosigns = useQuery({ queryKey: cosignsKey, queryFn: sessionCosigns, staleTime: Infinity });
  const approve = useMutation({
    // A passkey session co-signs the RecoveryApproval record first; a
    // cancelled dialog changes nothing.
    mutationFn: async () => {
      if (cosigns.data ?? (await sessionCosigns())) {
        const c = await requestCosign({
          purpose: 'recovery_approve',
          target: r.id,
          summary: t('approve.summary', { handle: `@${r.handle}` }),
        });
        if (!c) return false;
        await approveRecovery(r.id, c);
      } else {
        await approveRecovery(r.id);
      }
      return true;
    },
    // The item stays (approved) until the list's next refresh drops it.
    onSuccess: (done) => {
      if (done) void qc.invalidateQueries({ queryKey: requestsKey, refetchType: 'none' });
    },
  });
  const name = r.displayName ? `${r.displayName} (@${r.handle})` : `@${r.handle}`;
  return (
    <li className="recovery-item" data-testid="recovery-request" data-handle={r.handle}>
      <p>{t('approve.item', { name, at: formatDateTime(r.expiresAt, i18n.language) })}</p>
      <p className="muted">{t('approve.warning')}</p>
      <button
        type="button"
        data-testid="recovery-approve"
        disabled={approve.isPending || approve.data === true}
        onClick={() => {
          approve.mutate();
        }}
      >
        {t('approve.button')}
      </button>
      {approve.data === true ? (
        <p role="status" data-testid="recovery-approved">
          {t('approve.done')}
        </p>
      ) : null}
      <ErrorLine err={approve.error} />
    </li>
  );
}

// HelpFriend starts the in-person recovery of a friend+ friend.
function HelpFriend() {
  const { t } = useTranslation('recovery');
  const friends = useQuery({ queryKey: friendsKey, queryFn: fetchFriendPlus });
  const [opened, setOpened] = useState<{ friend: FriendVM; hs: OpenedVM } | undefined>();
  const start = useMutation({
    mutationFn: async (f: FriendVM) => ({ friend: f, hs: await startRecovery(f.ref) }),
    onSuccess: setOpened,
  });
  return (
    <section className="recovery-section" data-testid="recovery-help">
      <h2>{t('help.title')}</h2>
      <p>{t('help.intro')}</p>
      {opened ? (
        <>
          <p>{t('help.scan', { handle: `@${opened.friend.handle}` })}</p>
          <HandshakeHost
            key={opened.hs.hs}
            opened={opened.hs}
            onAgain={() => {
              start.mutate(opened.friend);
            }}
          />
          <button
            type="button"
            onClick={() => {
              setOpened(undefined);
            }}
          >
            {t('help.close')}
          </button>
        </>
      ) : (
        <>
          {friends.data && friends.data.length === 0 ? <p className="muted">{t('help.none')}</p> : null}
          <ul className="recovery-list">
            {friends.data?.map((f) => (
              <li key={f.ref} className="recovery-item" data-testid="recovery-friend" data-handle={f.handle}>
                <span>{f.displayName ? `${f.displayName} (@${f.handle})` : `@${f.handle}`}</span>
                <button
                  type="button"
                  data-testid="recovery-start"
                  disabled={start.isPending}
                  onClick={() => {
                    start.mutate(f);
                  }}
                >
                  {t('help.start')}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      <ErrorLine err={start.error ?? friends.error} />
    </section>
  );
}
