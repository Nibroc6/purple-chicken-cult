// The initiator's side of an in-person handshake (§7, plan P1-W02): invites
// (InvitePage) and the friend+ upgrades and recovery handshakes other features
// open (they render <HandshakeHost opened={toOpenedVM(view)} />).
//
// The device makes its ephemeral key and posts E_i, syncs its frame counter to
// the server's frame and then renders a new QR frame every 250 ms from the
// frame key. Once the other device claimed it, both screens show the SAS;
// after "codes match" this device listens for the sound (or, in dual-QR mode,
// shows the second, kmac-keyed QR), reports what it decoded and, when the
// outcome passed, co-signs the drafted record with a passkey.
// Owner: P1-W02.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import QRCode from 'qrcode';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { requestCosign } from '../cosign';
import {
  cancelHandshake,
  confirmHandshake,
  cosignHandshake,
  fetchFrame,
  fetchPeer,
  fetchStatus,
  postEpub,
  report,
  type OpenedVM,
} from './api';
import { audioInput, handshakeAudio, reportBody } from './audio';
import {
  b64url,
  commit,
  ephemeral,
  FRAME_MS,
  frameAt,
  fromB64url,
  kmac,
  parseFrame,
  renderDualFrame,
  renderFrame,
  type Ephemeral,
} from './handshake';
import { ErrorText, invitesKey } from './shared';
import { isLive, joinURL, passed, type StatusVM } from './vm';

// LISTEN_MS bounds one listen; the device keeps listening while the handshake waits for sound.
const LISTEN_MS = 15_000;

// Device is the initiator's device state once synced.
interface Device {
  eph: Ephemeral;
  key: Uint8Array;
  hs: Uint8Array;
  n0: number;
  t0: number;
}

function useDevice(opened: OpenedVM): { dev?: Device; err?: unknown } {
  const [out, setOut] = useState<{ dev?: Device; err?: unknown }>({});
  useEffect(() => {
    const run = { live: true };
    void (async () => {
      try {
        const eph = await ephemeral();
        await postEpub(opened.hs, b64url(eph.pub));
        const first = await fetchFrame(opened.hs);
        const f = parseFrame(first.payload);
        if (!f) throw new Error('frame');
        if (run.live) setOut({ dev: { eph, key: fromB64url(opened.frameKey), hs: f.hs, n0: f.n, t0: performance.now() } });
      } catch (err) {
        if (run.live) setOut({ err });
      }
    })();
    return () => {
      run.live = false;
    };
  }, [opened.hs, opened.frameKey]);
  return out;
}

// useKmac derives kmac once the other device's E_v is relayed.
function useKmac(opened: OpenedVM, dev: Device | undefined, ready: boolean): Uint8Array | undefined {
  const q = useQuery({
    queryKey: [...invitesKey, 'kmac', opened.hs],
    queryFn: async () => {
      if (!dev) throw new Error('device');
      return kmac(dev.eph, fromB64url(await fetchPeer(opened.hs)), dev.hs);
    },
    enabled: ready && dev !== undefined,
    staleTime: Infinity,
    gcTime: 0,
  });
  return q.data;
}

export function HandshakeHost({ opened, onDone, onAgain }: { opened: OpenedVM; onDone?: () => void; onAgain?: () => void }) {
  const { t } = useTranslation('invites');
  const qc = useQueryClient();
  const { dev, err: devErr } = useDevice(opened);
  const key = [...invitesKey, 'hs', opened.hs];
  const status = useQuery({
    queryKey: key,
    queryFn: () => fetchStatus(opened.hs),
    refetchInterval: (q) => (q.state.data && !isLive(q.state.data.state) ? false : 1000),
  });
  const st = status.data;
  const state = st?.state ?? 'open';
  const doneRef = useRef(false);
  useEffect(() => {
    if (state === 'done' && !doneRef.current) {
      doneRef.current = true;
      onDone?.();
    }
  }, [state, onDone]);
  const refresh = () => qc.invalidateQueries({ queryKey: key });
  const confirm = useMutation({ mutationFn: () => confirmHandshake(opened.hs), onSuccess: refresh });
  const cancel = useMutation({ mutationFn: () => cancelHandshake(opened.hs), onSuccess: refresh });
  const confirmed = state === 'confirmed';
  const k = useKmac(opened, dev, state === 'claimed' || confirmed);
  const kind = st?.kind ?? 'invite';

  return (
    <div className="invites-handshake" data-testid="invite-handshake" data-state={state} data-kind={kind}>
      {state === 'open' ? dev ? <Frames dev={dev} /> : <p className="muted">{t('hs.preparing')}</p> : null}
      {state === 'claimed' && st?.sas ? (
        <>
          <p>{t('hs.claimed')}</p>
          <p className="invites-sas" data-testid="invite-sas">
            {st.sas}
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
      {confirmed && st ? <Confirmed opened={opened} st={st} dev={dev} kmac={k} onChange={refresh} /> : null}
      {state === 'done' ? (
        <p role="status" data-testid="invite-done">
          {kind === 'upgrade' ? t('hs.doneUpgrade') : kind === 'recovery' ? t('hs.doneRecovery') : t('hs.done')}
        </p>
      ) : null}
      {state === 'cancelled' || state === 'expired' ? (
        <>
          <p role="status">{state === 'cancelled' ? t('hs.cancelled') : t('hs.expired')}</p>
          {onAgain ? (
            <button type="button" data-testid="invite-again" onClick={onAgain}>
              {t('hs.again')}
            </button>
          ) : null}
        </>
      ) : null}
      {state === 'open' || confirmed ? (
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
      <ErrorText err={devErr ?? confirm.error ?? cancel.error} />
    </div>
  );
}

// useFrameCounter re-renders on every frame boundary and returns the frame number.
function useFrameCounter(n0: number, t0: number): number {
  const [n, setN] = useState(n0);
  useEffect(() => {
    const id = setInterval(() => {
      setN(frameAt(n0, t0, performance.now()));
    }, FRAME_MS / 2);
    return () => {
      clearInterval(id);
    };
  }, [n0, t0]);
  return n;
}

function QR({ payload, label, testid }: { payload?: string; label: string; testid: string }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    if (!payload || !canvas.current) return;
    QRCode.toCanvas(canvas.current, payload, { errorCorrectionLevel: 'M', margin: 2, width: 320 }).catch(() => undefined);
  }, [payload]);
  return <canvas ref={canvas} className="invites-qr" role="img" aria-label={label} data-testid={testid} />;
}

// Frames shows the rotating QR code the other device scans.
function Frames({ dev }: { dev: Device }) {
  const { t } = useTranslation('invites');
  const n = useFrameCounter(dev.n0, dev.t0);
  const [payload, setPayload] = useState<string>();
  useEffect(() => {
    let live = true;
    void renderFrame(dev.key, { hs: dev.hs, ei: dev.eph.pub, n }).then((p) => {
      if (live) setPayload(p);
    });
    return () => {
      live = false;
    };
  }, [dev, n]);
  return (
    <>
      <QR payload={payload} label={t('hs.qrLabel')} testid="invite-qr" />
      <p>{t('hs.scanHint', { url: joinURL(location.origin) })}</p>
    </>
  );
}

// Confirmed runs the sound (or dual QR) and the co-signing after "codes match".
function Confirmed({
  opened,
  st,
  dev,
  kmac: k,
  onChange,
}: {
  opened: OpenedVM;
  st: StatusVM;
  dev?: Device;
  kmac?: Uint8Array;
  onChange: () => unknown;
}) {
  const { t } = useTranslation('invites');
  const ok = passed(st) && !(st.kind === 'upgrade' && st.outcome !== 'verified');
  return (
    <>
      {!ok && st.mode === 'dual_qr' && dev && k ? <DualFrames dev={dev} kmac={k} /> : null}
      {!ok && st.mode !== 'dual_qr' && k ? <Listener opened={opened} st={st} kmac={k} onReported={onChange} /> : null}
      {st.outcome ? (
        <p role="status" data-testid="invite-outcome" data-outcome={st.outcome}>
          {st.outcome === 'verified'
            ? t('outcome.verified')
            : st.outcome === 'accepted'
              ? t('outcome.accepted')
              : st.hint === 'try_jingle'
                ? t('outcome.retryJingle')
                : st.hint === 'volume_up'
                  ? t('outcome.retryVolume')
                  : t('outcome.retry')}
        </p>
      ) : null}
      {!ok && st.attemptsLeft === 0 ? <p role="alert">{t('outcome.noAttempts')}</p> : null}
      {ok && st.kind !== 'recovery' ? <Signing opened={opened} st={st} onSigned={onChange} /> : null}
    </>
  );
}

// Listener listens while the other device may play, and reports every decode.
function Listener({
  opened,
  st,
  kmac: k,
  onReported,
}: {
  opened: OpenedVM;
  st: StatusVM;
  kmac: Uint8Array;
  onReported: () => unknown;
}) {
  const { t } = useTranslation('invites');
  const [err, setErr] = useState<unknown>();
  const active = st.attemptsLeft > 0 || st.playing;
  const refresh = useRef(onReported);
  useEffect(() => {
    refresh.current = onReported;
  });
  useEffect(() => {
    if (!active) return;
    const run = { live: true };
    const alive = () => run.live;
    void (async () => {
      const c = await commit(k);
      while (alive()) {
        try {
          const rep = await handshakeAudio().listen({ timeoutMs: LISTEN_MS, input: audioInput() });
          if (!alive()) return;
          const now = await fetchStatus(opened.hs);
          if (!now.playing) {
            if (!isLive(now.state) || passed(now)) return;
            await new Promise((r) => setTimeout(r, FRAME_MS));
            continue;
          }
          await report(opened.hs, reportBody(rep, c));
          await refresh.current();
        } catch (e) {
          if (alive()) setErr(e);
          return;
        }
      }
    })();
    return () => {
      run.live = false;
    };
  }, [active, k, opened.hs]);
  if (!active) return null;
  return (
    <>
      <p role="status" data-testid="invite-listening">
        {st.quiet ? t('sound.listeningQuiet') : t('sound.listening')}
      </p>
      {err ? (
        <p className="invites-error" role="alert">
          {t('sound.noMic')}
        </p>
      ) : null}
    </>
  );
}

// DualFrames shows the second, kmac-keyed QR code (dual-QR mode).
function DualFrames({ dev, kmac: k }: { dev: Device; kmac: Uint8Array }) {
  const { t } = useTranslation('invites');
  const n = useFrameCounter(dev.n0, dev.t0);
  const [payload, setPayload] = useState<string>();
  useEffect(() => {
    let live = true;
    void renderDualFrame(k, dev.hs, n).then((p) => {
      if (live) setPayload(p);
    });
    return () => {
      live = false;
    };
  }, [dev, k, n]);
  return (
    <>
      <p>{t('dual.hint')}</p>
      <QR payload={payload} label={t('dual.qrLabel')} testid="invite-dual-qr" />
    </>
  );
}

// Signing co-signs the drafted record (the Invite, or the friend+ Friendship
// of an upgrade) once the server asks for it; the dialog opens by itself.
function Signing({ opened, st, onSigned }: { opened: OpenedVM; st: StatusVM; onSigned: () => unknown }) {
  const { t } = useTranslation('invites');
  const purpose = st.kind === 'upgrade' ? 'friend_upgrade' : 'invite_inviter';
  const sign = useMutation({
    mutationFn: async () => {
      const r = await requestCosign({
        purpose,
        target: opened.hs,
        summary: st.kind === 'upgrade' ? t('sign.summaryUpgrade') : t('sign.summaryInvite'),
      });
      if (!r) return false;
      await cosignHandshake(opened.hs, r.intent, r.assertion);
      return true;
    },
    onSettled: onSigned,
  });
  const asked = useRef(false);
  const needed = st.sign === 'needed';
  useEffect(() => {
    if (needed && !asked.current) {
      asked.current = true;
      sign.mutate();
    }
  }, [needed, sign]);
  if (st.sign === 'waiting') {
    return (
      <p role="status" data-testid="invite-sign-waiting">
        {st.kind === 'upgrade' ? t('sign.waitingFriend') : t('sign.waitingInvitee')}
      </p>
    );
  }
  if (!needed) {
    return (
      <p role="status" data-testid="invite-registering">
        {t('hs.confirmed')}
      </p>
    );
  }
  return (
    <>
      <p>{st.kind === 'upgrade' ? t('sign.askUpgrade') : t('sign.askInvite')}</p>
      <button
        type="button"
        data-testid="invite-sign"
        disabled={sign.isPending}
        onClick={() => {
          sign.mutate();
        }}
      >
        {t('sign.button')}
      </button>
      <ErrorText err={sign.error} />
    </>
  );
}
