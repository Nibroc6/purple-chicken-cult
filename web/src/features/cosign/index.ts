// Public surface of the cosign feature for the features whose writes need a
// passkey co-signature. Owner: P1-W01.
export { requestCosign } from './store';
export type { CosignRequest, CosignResult } from './store';
export type { CosignPurpose } from './api';
