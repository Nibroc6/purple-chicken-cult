// The main navigation (SCAFFOLD-owned list): every entry of the sidebar, or of
// the top header when a user picks that layout. A feature adds its page here.
// moreNav is the smaller list under it (about, terms and moderation pages, the old
// site footer); signed out, or in the top-header layout on wide screens, it
// renders as a page footer instead. Labels live in the common namespace.
// i18n-dynamic: common:nav.
export type NavAudience = 'everyone' | 'member' | 'moderator' | 'admin';

export interface MainNavEntry {
  to: string;
  label:
    | 'nav.feed'
    | 'nav.compose'
    | 'nav.invite'
    | 'nav.people'
    | 'nav.saves'
    | 'nav.seen'
    | 'nav.notifications'
    | 'nav.moderation'
    | 'nav.settings'
    | 'nav.about'
    | 'nav.terms'
    | 'nav.modLog'
    | 'nav.blame'
    | 'nav.modRecovery'
    | 'nav.roles';
  // A short glyph shown when the sidebar is collapsed (decorative).
  icon: string;
  // The feature nav badges render next to this entry (e.g. the unread count).
  badges?: boolean;
  // Who sees the entry (default: signed-in members).
  who?: NavAudience;
}

export const mainNav: MainNavEntry[] = [
  { to: '/', label: 'nav.feed', icon: '⌂' },
  { to: '/compose', label: 'nav.compose', icon: '✎' },
  { to: '/invite', label: 'nav.invite', icon: '✉' },
  { to: '/friends', label: 'nav.people', icon: '☺' },
  { to: '/saves', label: 'nav.saves', icon: '★' },
  { to: '/seen', label: 'nav.seen', icon: '◉' },
  { to: '/notifications', label: 'nav.notifications', icon: '♪', badges: true },
  { to: '/mod', label: 'nav.moderation', icon: '⚑', who: 'moderator' },
  { to: '/settings', label: 'nav.settings', icon: '⚙' },
];

export const moreNav: MainNavEntry[] = [
  { to: '/about', label: 'nav.about', icon: '?', who: 'everyone' },
  { to: '/terms', label: 'nav.terms', icon: '§', who: 'everyone' },
  { to: '/mod/log', label: 'nav.modLog', icon: '≡' },
  { to: '/blame', label: 'nav.blame', icon: '⚠' },
  { to: '/mod/recovery', label: 'nav.modRecovery', icon: '⟲', who: 'moderator' },
  { to: '/mod/roles', label: 'nav.roles', icon: '♛', who: 'admin' },
];

// navAllows reports whether a viewer sees an entry.
export function navAllows(e: MainNavEntry, signedIn: boolean, role: string | undefined): boolean {
  switch (e.who ?? 'member') {
    case 'everyone':
      return true;
    case 'member':
      return signedIn;
    case 'moderator':
      return signedIn && (role === 'moderator' || role === 'admin');
    case 'admin':
      return signedIn && role === 'admin';
  }
}
