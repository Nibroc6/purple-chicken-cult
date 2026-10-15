// Shell slots of the notifications feature. Owner: P0-W10 → P1-W05.
import type { FeatureSlots } from '../../app/slots';

// NotificationsBadge renders nothing until the owning work package lands.
function NotificationsBadge() {
  return null;
}

export const slots: FeatureSlots = { navBadge: NotificationsBadge };
