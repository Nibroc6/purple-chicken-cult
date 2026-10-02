// Shell slots of the settings feature. Owner: P0-W11 → P1-W10.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import type { FeatureSlots } from '../../app/slots';
import { useMe } from '../../app/session';
import { applyA11y } from '../../lib/a11y';
import { formatDateTime } from '../../lib/time';
import { cancelRecovery, fetchRecoveryPending } from './api';
import { ErrorText, useAccountMe } from './shared';
import { toA11y, toUIPrefs } from './vm';

export const recoveryKey = ['me', 'recovery'] as const;

// RecoveryBanner warns every live session of an account with a pending manual
// recovery and lets it cancel (§10.3, security #14).
export function RecoveryBanner() {
  const { t, i18n } = useTranslation('account');
  const me = useMe();
  const qc = useQueryClient();
  const pending = useQuery({
    queryKey: recoveryKey,
    queryFn: fetchRecoveryPending,
    enabled: me !== undefined,
    refetchInterval: 5 * 60_000,
  });
  const cancel = useMutation({
    mutationFn: cancelRecovery,
    onSuccess: () => qc.setQueryData(recoveryKey, null),
  });
  if (!me) return null;
  if (cancel.isSuccess) {
    return (
      <p role="status" className="recovery-banner">
        {t('banner.cancelled')}
      </p>
    );
  }
  const at = pending.data;
  if (!at) return null;
  return (
    <div role="alert" className="recovery-banner">
      <p>{t('banner.pending', { at: formatDateTime(at, i18n.language) })}</p>
      <button
        type="button"
        disabled={cancel.isPending}
        onClick={() => {
          cancel.mutate();
        }}
      >
        {t('banner.cancel')}
      </button>
      <ErrorText err={cancel.error} />
    </div>
  );
}

// PrefsApplier applies the signed-in user's accessibility switches to <html>
// on every page (§15.1: accessibility is not a format).
export function PrefsApplier() {
  const me = useAccountMe();
  const raw = me.data?.settings.ui_prefs;
  useEffect(() => {
    if (raw !== undefined) applyA11y(toA11y(toUIPrefs(raw)));
  }, [raw]);
  return null;
}

export const slots: FeatureSlots = { global: [RecoveryBanner, PrefsApplier] };
