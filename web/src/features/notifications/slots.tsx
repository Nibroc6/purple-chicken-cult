// Shell slots of the notifications feature. Owner: P0-W10 → P1-W05.
import { useTranslation } from 'react-i18next';
import type { FeatureSlots } from '../../app/slots';
import { useUnread } from './unread';

// NotificationsBadge shows the unread count next to the nav entry (live over
// SSE; nothing when there is nothing unread).
export function NotificationsBadge() {
  const { t } = useTranslation('notifications');
  const n = useUnread();
  if (!n) return null;
  return (
    <span className="nav-badge" data-testid="notifications-badge" data-count={n} aria-label={t('badge.label', { count: n })}>
      {n >= 100 ? t('badge.many') : t('badge.count', { count: n })}
    </span>
  );
}

export const slots: FeatureSlots = { navBadge: NotificationsBadge };
