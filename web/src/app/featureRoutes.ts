// Every feature's routes array, pre-listed by the scaffold (features never edit it).
import type { RouteObject } from 'react-router';
import { routes as feed } from '../features/feed/routes';
import { routes as landing } from '../features/landing/routes';
import { routes as auth } from '../features/auth/routes';
import { routes as stepup } from '../features/stepup/routes';
import { routes as join } from '../features/join/routes';
import { routes as invites } from '../features/invites/routes';
import { routes as composer } from '../features/composer/routes';
import { routes as post } from '../features/post/routes';
import { routes as tree } from '../features/tree/routes';
import { routes as people } from '../features/people/routes';
import { routes as notifications } from '../features/notifications/routes';
import { routes as seen } from '../features/seen/routes';
import { routes as engage } from '../features/engage/routes';
import { routes as settings } from '../features/settings/routes';
import { routes as modrecovery } from '../features/modrecovery/routes';
import { routes as moderation } from '../features/moderation/routes';
import { routes as recovery } from '../features/recovery/routes';
import { routes as saves } from '../features/saves/routes';
import { routes as themes } from '../features/themes/routes';
import { routes as push } from '../features/push/routes';
import { routes as media } from '../features/media/routes';
import { routes as cosign } from '../features/cosign/routes';
import { routes as handshakeAudio } from '../features/handshake-audio/routes';

export const featureRoutes: RouteObject[] = [
  ...feed,
  ...landing,
  ...auth,
  ...stepup,
  ...join,
  ...invites,
  ...composer,
  ...post,
  ...tree,
  ...people,
  ...notifications,
  ...seen,
  ...engage,
  ...settings,
  ...modrecovery,
  ...moderation,
  ...recovery,
  ...saves,
  ...themes,
  ...push,
  ...media,
  ...cosign,
  ...handshakeAudio,
];
