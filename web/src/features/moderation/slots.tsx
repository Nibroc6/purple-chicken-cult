// Shell slots of the moderation feature. Owner: P0-W12 → P1-W06. Its links (the
// terms, the moderation log, the blame inbox, the moderator and admin pages)
// live in the shell's secondary navigation (app/mainNav.ts moreNav). The
// report modal is a global slot.
import type { FeatureSlots } from '../../app/slots';
import { ReportModal } from './ReportModal';

export const slots: FeatureSlots = { global: [ReportModal] };
