// Shell slots of the stepup feature. Owner: P0-W01 → P1-W07.
import type { FeatureSlots } from '../../app/slots';

// StepUpModal renders nothing until the owning work package lands.
function StepUpModal() {
  return null;
}

export const slots: FeatureSlots = { global: [StepUpModal] };
