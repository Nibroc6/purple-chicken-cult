// Shell slots of the settings feature. Owner: P0-W11 → P1-W10.
import type { FeatureSlots } from '../../app/slots';

// RecoveryBanner renders nothing until the owning work package lands.
function RecoveryBanner() {
  return null;
}

export const slots: FeatureSlots = { global: [RecoveryBanner] };
