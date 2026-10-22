// The main navigation (SCAFFOLD-owned list): every entry of the sidebar, or of
// the top header when a user picks that layout. A feature adds its page here.
// Labels live in the common namespace. i18n-dynamic: common:nav.
export interface MainNavEntry {
  to: string;
  label: 'nav.feed' | 'nav.compose' | 'nav.invite' | 'nav.people' | 'nav.saves' | 'nav.seen' | 'nav.notifications' | 'nav.moderation' | 'nav.settings';
  // A short glyph shown when the sidebar is collapsed (decorative).
  icon: string;
  // The feature nav badges render next to this entry (e.g. the unread count).
  badges?: boolean;
  // Shown to moderators and admins only.
  moderator?: boolean;
}

export const mainNav: MainNavEntry[] = [
  { to: '/', label: 'nav.feed', icon: '⌂' },
  { to: '/compose', label: 'nav.compose', icon: '✎' },
  { to: '/invite', label: 'nav.invite', icon: '✉' },
  { to: '/friends', label: 'nav.people', icon: '☺' },
  { to: '/saves', label: 'nav.saves', icon: '★' },
  { to: '/seen', label: 'nav.seen', icon: '◉' },
  { to: '/notifications', label: 'nav.notifications', icon: '♪', badges: true },
  { to: '/mod', label: 'nav.moderation', icon: '⚑', moderator: true },
  { to: '/settings', label: 'nav.settings', icon: '⚙' },
];
