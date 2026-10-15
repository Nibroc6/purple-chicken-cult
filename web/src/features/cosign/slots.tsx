// Shell slots of the cosign feature. Owner: P1-W01.
import type { FeatureSlots } from '../../app/slots';

// CosignDialog renders nothing until the owning work package lands.
function CosignDialog() {
  return null;
}

export const slots: FeatureSlots = { global: [CosignDialog] };
