// Every settings page, P0 and P1 (SCAFFOLD-owned list; plan §6.4). The settings
// index links to all of them. Labels live in the common namespace.
// i18n-dynamic: common:settingsNav.
export interface SettingsNavEntry {
  path: string;
  label:
    | 'settingsNav.display'
    | 'settingsNav.languages'
    | 'settingsNav.privacy'
    | 'settingsNav.passkeys'
    | 'settingsNav.blocks'
    | 'settingsNav.hidden'
    | 'settingsNav.export'
    | 'settingsNav.delete'
    | 'settingsNav.recovery'
    | 'settingsNav.theme'
    | 'settingsNav.notifications';
  phase: 0 | 1;
}

// Phase 1 entries are enabled (SCAFFOLD-1 has landed).
export const phase1Enabled = true;

export const settingsNav: SettingsNavEntry[] = [
  { path: '/settings/display', label: 'settingsNav.display', phase: 0 },
  { path: '/settings/languages', label: 'settingsNav.languages', phase: 0 },
  { path: '/settings/privacy', label: 'settingsNav.privacy', phase: 0 },
  { path: '/settings/passkeys', label: 'settingsNav.passkeys', phase: 0 },
  { path: '/settings/blocks', label: 'settingsNav.blocks', phase: 0 },
  { path: '/settings/hidden', label: 'settingsNav.hidden', phase: 0 },
  { path: '/settings/export', label: 'settingsNav.export', phase: 0 },
  { path: '/settings/delete', label: 'settingsNav.delete', phase: 0 },
  { path: '/settings/recovery', label: 'settingsNav.recovery', phase: 1 },
  { path: '/settings/theme', label: 'settingsNav.theme', phase: 1 },
  { path: '/settings/notifications', label: 'settingsNav.notifications', phase: 1 },
];

export const visibleSettingsNav = settingsNav.filter((e) => e.phase === 0 || phase1Enabled);
