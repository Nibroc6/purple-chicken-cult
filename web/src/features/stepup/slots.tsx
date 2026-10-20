// Shell slots of the stepup feature: the global step-up modal and the client
// signal reporter. Owner: P0-W01 → P1-W07.
import type { FeatureSlots } from '../../app/slots';
import { SignalsReporter } from './SignalsReporter';
import { StepUpModal } from './StepUpModal';

export const slots: FeatureSlots = { global: [StepUpModal, SignalsReporter] };
