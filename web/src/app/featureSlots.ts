// Every feature's shell slots, pre-listed by the scaffold.
import type { FeatureSlots } from './slots';
import { slots as feed } from '../features/feed/slots';
import { slots as landing } from '../features/landing/slots';
import { slots as auth } from '../features/auth/slots';
import { slots as stepup } from '../features/stepup/slots';
import { slots as join } from '../features/join/slots';
import { slots as invites } from '../features/invites/slots';
import { slots as composer } from '../features/composer/slots';
import { slots as post } from '../features/post/slots';
import { slots as tree } from '../features/tree/slots';
import { slots as people } from '../features/people/slots';
import { slots as notifications } from '../features/notifications/slots';
import { slots as seen } from '../features/seen/slots';
import { slots as engage } from '../features/engage/slots';
import { slots as settings } from '../features/settings/slots';
import { slots as modrecovery } from '../features/modrecovery/slots';
import { slots as moderation } from '../features/moderation/slots';
import { slots as recovery } from '../features/recovery/slots';
import { slots as saves } from '../features/saves/slots';
import { slots as themes } from '../features/themes/slots';
import { slots as push } from '../features/push/slots';
import { slots as media } from '../features/media/slots';
import { slots as cosign } from '../features/cosign/slots';
import { slots as handshakeAudio } from '../features/handshake-audio/slots';
import { slots as messages } from '../features/messages/slots';

export const featureSlots: FeatureSlots[] = [
  feed,
  landing,
  auth,
  stepup,
  join,
  invites,
  composer,
  post,
  tree,
  people,
  notifications,
  seen,
  engage,
  settings,
  modrecovery,
  moderation,
  recovery,
  saves,
  themes,
  push,
  media,
  cosign,
  handshakeAudio,
  messages,
];
