// Shell slots of the direct-messages feature: the unread badge next to the
// Messages entry and the share sheet (?share=<post ref> over any page).
// Owner: direct messages (contract request dm-1).
import { useTranslation } from 'react-i18next';
import type { FeatureSlots } from '../../app/slots';
import { ShareModal } from './ShareModal';
import { useUnread } from './unread';
import './badge.css';

export function MessagesBadge() {
  const { t } = useTranslation('messages');
  const n = useUnread();
  if (!n) return null;
  return (
    <span className="nav-badge" data-testid="messages-badge" data-count={n} aria-label={t('badge.label', { count: n })}>
      {n >= 100 ? t('badge.many') : t('badge.count', { count: n })}
    </span>
  );
}

export const slots: FeatureSlots = { navBadge: MessagesBadge, navBadgeFor: '/messages', global: [ShareModal] };
