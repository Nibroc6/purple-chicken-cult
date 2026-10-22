// Type-checked i18n keys (SCAFFOLD contract): a missing key is a TypeScript error.
// Add a namespace here AND in namespaces.ts (contract-steward change).
import 'i18next';
import type common from './locales/en/common.json';
import type errors from './locales/en/errors.json';
import type auth from './locales/en/auth.json';
import type stepup from './locales/en/stepup.json';
import type people from './locales/en/people.json';
import type invites from './locales/en/invites.json';
import type join from './locales/en/join.json';
import type post from './locales/en/post.json';
import type composer from './locales/en/composer.json';
import type engage from './locales/en/engage.json';
import type seen from './locales/en/seen.json';
import type feed from './locales/en/feed.json';
import type tree from './locales/en/tree.json';
import type markdown from './locales/en/markdown.json';
import type notifications from './locales/en/notifications.json';
import type integrity from './locales/en/integrity.json';
import type settings from './locales/en/settings.json';
import type account from './locales/en/account.json';
import type modrecovery from './locales/en/modrecovery.json';
import type moderation from './locales/en/moderation.json';
import type landing from './locales/en/landing.json';
import type handshake from './locales/en/handshake.json';
import type cosign from './locales/en/cosign.json';
import type media from './locales/en/media.json';
import type push from './locales/en/push.json';
import type signals from './locales/en/signals.json';
import type themes from './locales/en/themes.json';
import type explorer from './locales/en/explorer.json';
import type recovery from './locales/en/recovery.json';
import type saves from './locales/en/saves.json';
import type about from './locales/en/about.json';
import type messages from './locales/en/messages.json';

declare module 'i18next' {
  interface CustomTypeOptions {
    defaultNS: 'common';
    resources: {
      common: typeof common;
      errors: typeof errors;
      auth: typeof auth;
      stepup: typeof stepup;
      people: typeof people;
      invites: typeof invites;
      join: typeof join;
      post: typeof post;
      composer: typeof composer;
      engage: typeof engage;
      seen: typeof seen;
      feed: typeof feed;
      tree: typeof tree;
      markdown: typeof markdown;
      notifications: typeof notifications;
      integrity: typeof integrity;
      settings: typeof settings;
      account: typeof account;
      modrecovery: typeof modrecovery;
      moderation: typeof moderation;
      landing: typeof landing;
      handshake: typeof handshake;
      cosign: typeof cosign;
      media: typeof media;
      push: typeof push;
      signals: typeof signals;
      themes: typeof themes;
      explorer: typeof explorer;
      recovery: typeof recovery;
      saves: typeof saves;
      about: typeof about;
      messages: typeof messages;
    };
  }
}
