// Cosign API call (plan §5.3 cosignapi). Owner: P1-W01.
import { api, unwrap } from '../../api/client';
import type { components } from '../../api/schema.gen';

export type CosignPurpose = components['schemas']['CosignBeginIn']['purpose'];
export type CosignIntentJSON = components['schemas']['CosignIntent'];

export async function cosignBegin(purpose: CosignPurpose, target?: string): Promise<CosignIntentJSON> {
  return unwrap(await api.POST('/cosign', { body: target ? { purpose, target } : { purpose } }));
}
