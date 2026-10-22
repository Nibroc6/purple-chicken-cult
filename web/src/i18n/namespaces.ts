// The i18n namespace list (SCAFFOLD contract). One owner per namespace (plan §6.5);
// each namespace is web/src/i18n/locales/en/<ns>.json. Adding one is a
// contract-steward change (also add it to resources.d.ts).
export const namespaces = [
  'common',
  'errors',
  'auth',
  'stepup',
  'people',
  'invites',
  'join',
  'post',
  'composer',
  'engage',
  'seen',
  'feed',
  'tree',
  'markdown',
  'notifications',
  'integrity',
  'settings',
  'account',
  'modrecovery',
  'moderation',
  'landing',
  'handshake',
  'cosign',
  'media',
  'push',
  'signals',
  'themes',
  'explorer',
  'recovery',
  'saves',
  'about',
  'messages',
] as const;

export type Namespace = (typeof namespaces)[number];
