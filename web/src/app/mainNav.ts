// The main navigation (SCAFFOLD-owned list): every entry of the sidebar, or of
// the top header when a user picks that layout. A feature adds its page here.
// Labels live in the common namespace. i18n-dynamic: common:nav.
import type { IconName } from '../lib/Icon';

export interface MainNavEntry {
  to: string;
  label: 'nav.feed' | 'nav.compose' | 'nav.invite' | 'nav.people' | 'nav.saves' | 'nav.seen' | 'nav.notifications' | 'nav.moderation' | 'nav.settings' | 'nav.about';
  // The icon shown next to the label, and alone when the sidebar is collapsed
  // (decorative).
  icon: IconName;
  // The feature nav badges render next to this entry (e.g. the unread count).
  badges?: boolean;
  // Shown to moderators and admins only.
  moderator?: boolean;
}

export const mainNav: MainNavEntry[] = [
  { to: '/', label: 'nav.feed', icon: 'feed' },
  { to: '/compose', label: 'nav.compose', icon: 'compose' },
  { to: '/invite', label: 'nav.invite', icon: 'invite' },
  { to: '/friends', label: 'nav.people', icon: 'people' },
  { to: '/saves', label: 'nav.saves', icon: 'saves' },
  { to: '/seen', label: 'nav.seen', icon: 'seen' },
  { to: '/notifications', label: 'nav.notifications', icon: 'notifications', badges: true },
  { to: '/mod', label: 'nav.moderation', icon: 'moderation', moderator: true },
  { to: '/settings', label: 'nav.settings', icon: 'settings' },
  { to: '/about', label: 'nav.about', icon: 'about' },
];
