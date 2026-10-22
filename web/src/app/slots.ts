// Shell slot contract (SCAFFOLD): each feature's slots.tsx exports `slots`.
// navBadge renders next to the feature's nav entry (e.g. the unread count);
// global components mount once in the shell (modals, banners).
import type { ComponentType } from 'react';

export interface FeatureSlots {
  navBadge?: ComponentType;
  // navBadgeFor is the nav entry (its path) the badge belongs to; without it
  // the badge renders next to the notifications entry.
  navBadgeFor?: string;
  global?: ComponentType[];
}
