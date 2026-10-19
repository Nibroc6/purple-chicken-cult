// Push notification settings for this device (P1, ARCHITECTURE §10.6): turn
// Web Push on or off, and hide notification content on the lock screen. The
// server's state of this device's subscription is what counts: logging out
// removes it, and another account subscribing on this browser moves it.
// Owner: P1-W05.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { ApiError, errorKey } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { fetchKey, fetchState, removeSubscription, saveSubscription, type DeviceSubscription } from './api';
import { getDevicePush, PushUnavailable } from './device';
import './push.css';

const pushCodes = new Set(['push_endpoint_invalid', 'push_keys_invalid', 'feature_disabled', 'rate_limited']);

// pushErrorKey is push:errors.<code> for the codes this page explains, else
// the shared errors catalog.
// i18n-dynamic: push:errors.
export function pushErrorKey(err: unknown): string {
  if (err instanceof PushUnavailable) return 'push:errors.push_unavailable';
  if (err instanceof ApiError && pushCodes.has(err.code)) return `push:errors.${err.code}`;
  if (err instanceof DOMException) return 'push:errors.push_unavailable';
  return errorKey(err);
}

function ErrorLine({ err }: { err: unknown }) {
  const { t } = useTranslation(['push', 'errors']);
  if (!err) return null;
  return (
    <p className="push-error" role="alert" data-testid="push-error">
      {t(pushErrorKey(err) as 'errors:unknown')}
    </p>
  );
}

export const pushKeyQuery = ['push', 'key'] as const;

function DeviceSettings({ vapidKey }: { vapidKey: string }) {
  const { t } = useTranslation('push');
  const id = useId();
  const qc = useQueryClient();
  const dev = getDevicePush();
  const [perm, setPerm] = useState<NotificationPermission>(() => dev.permission());
  const [sub, setSub] = useState<DeviceSubscription | null | undefined>(undefined);
  const [hideSet, setHide] = useState<boolean | null>(null);
  const [devErr, setDevErr] = useState<unknown>(null);

  useEffect(() => {
    let live = true;
    dev
      .current()
      .then((s) => {
        if (live) setSub(s);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setSub(null);
        setDevErr(e);
      });
    return () => {
      live = false;
    };
  }, [dev]);

  const stateKey = ['push', 'state', sub?.endpoint ?? ''] as const;
  const state = useQuery({
    queryKey: stateKey,
    queryFn: () => fetchState(sub?.endpoint ?? ''),
    enabled: !!sub,
  });
  const subscribed = !!sub && state.data?.subscribed === true;
  const hide = hideSet ?? state.data?.hide_content ?? false;

  const refresh = (s: DeviceSubscription | null) => qc.invalidateQueries({ queryKey: ['push', 'state', s?.endpoint ?? ''] });

  const enable = useMutation({
    mutationFn: async () => {
      setDevErr(null);
      let p = dev.permission();
      if (p !== 'granted') p = await dev.requestPermission();
      setPerm(p);
      if (p !== 'granted') return null;
      const s = sub ?? (await dev.subscribe(vapidKey));
      await saveSubscription(s, hide);
      return s;
    },
    onSuccess: async (s) => {
      if (!s) return;
      setSub(s);
      await refresh(s);
    },
  });
  const disable = useMutation({
    mutationFn: async () => {
      if (!sub) return;
      await removeSubscription(sub.endpoint);
      await dev.unsubscribe();
    },
    onSuccess: async () => {
      const old = sub ?? null;
      setSub(null);
      await refresh(old);
    },
  });
  const saveHide = useMutation({
    mutationFn: async (v: boolean) => {
      if (sub && subscribed) await saveSubscription(sub, v);
    },
    onSuccess: async () => {
      await refresh(sub ?? null);
    },
  });

  if (sub === undefined || (sub && state.isPending)) return <Loading />;
  const busy = enable.isPending || disable.isPending || saveHide.isPending;
  return (
    <div className="push-settings" data-testid="push-settings" data-subscribed={subscribed}>
      {perm === 'denied' ? (
        <p role="status" data-testid="push-denied">
          {t('device.denied')}
        </p>
      ) : null}
      <p role="status" data-testid="push-status">
        {subscribed ? t('device.on') : t('device.off')}
      </p>
      <div className="push-field">
        <label className="push-choice">
          <input
            type="checkbox"
            data-testid="push-hide"
            checked={hide}
            disabled={busy}
            aria-describedby={`${id}-hide`}
            onChange={(e) => {
              setHide(e.target.checked);
              saveHide.mutate(e.target.checked);
            }}
          />
          {t('hide.label')}
        </label>
        <small id={`${id}-hide`} className="muted">
          {t('hide.help')}
        </small>
      </div>
      <div className="push-actions">
        {subscribed ? (
          <button
            type="button"
            data-testid="push-disable"
            disabled={busy}
            onClick={() => {
              disable.mutate();
            }}
          >
            {t('device.turnOff')}
          </button>
        ) : (
          <button
            type="button"
            data-testid="push-enable"
            disabled={busy || perm === 'denied'}
            onClick={() => {
              enable.mutate();
            }}
          >
            {enable.isPending ? t('device.turningOn') : t('device.turnOn')}
          </button>
        )}
      </div>
      <ErrorLine err={enable.error ?? disable.error ?? saveHide.error ?? state.error ?? devErr} />
    </div>
  );
}

export default function PushSettingsPage() {
  const { t } = useTranslation(['push', 'settings']);
  const supported = getDevicePush().supported();
  const key = useQuery({ queryKey: pushKeyQuery, queryFn: fetchKey });
  let body;
  if (!supported) {
    body = (
      <p role="status" data-testid="push-unsupported">
        {t('push:device.unsupported')}
      </p>
    );
  } else if (key.isPending) {
    body = <Loading />;
  } else if (key.error) {
    body = <ErrorLine err={key.error} />;
  } else if (!key.data.enabled || !key.data.key) {
    body = (
      <p role="status" data-testid="push-server-disabled">
        {t('push:server.disabled')}
      </p>
    );
  } else {
    body = <DeviceSettings vapidKey={key.data.key} />;
  }
  return (
    <section className="page push-page" data-testid="push-page">
      <h1>{t('push:page.title')}</h1>
      <p>
        <Link to="/settings">{t('settings:index.title')}</Link>
      </p>
      <p>{t('push:page.intro')}</p>
      {body}
    </section>
  );
}
