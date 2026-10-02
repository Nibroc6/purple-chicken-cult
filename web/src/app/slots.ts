// Shell slot contract (SCAFFOLD): each feature's slots.tsx exports `slots`.
// navBadge renders next to the feature's nav entry (e.g. the unread count);
// global components mount once in the shell (modals, banners).
import type { ComponentType } from 'react';

export interface FeatureSlots {
  navBadge?: ComponentType;
  global?: ComponentType[];
}
