// Co-sign API calls (plan §5.3 cosignapi). Owner: P1-W01.
import { api, unwrap } from '../../api/client';
import type { components } from '../../api/schema.gen';

export type IntentJSON = components['schemas']['CosignIntent'];

// beginCosign opens an intent: the exact record to show and the passkey get
// options whose challenge covers it.
export async function beginCosign(purpose: string, target?: string): Promise<IntentJSON> {
  return unwrap(await api.POST('/cosign', { body: target ? { purpose, target } : { purpose } }));
}
