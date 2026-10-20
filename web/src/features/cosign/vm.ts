// View-model of the record a co-sign intent asks the person to sign. Owner: P1-W01.
import type { CosignIntentJSON } from './api';

export interface CosignRecordVM {
  kind: string;
  issuedAt: Date | undefined;
  // The exact record as the passkey signs it (canonical JSON, pretty-printed
  // for reading; the server signs the compact form of the same object).
  json: string;
}

export function toRecordVM(intent: CosignIntentJSON): CosignRecordVM {
  const rec = intent.record;
  const at = typeof rec.issued_at === 'string' ? new Date(rec.issued_at) : undefined;
  return {
    kind: rec.kind,
    issuedAt: at && !Number.isNaN(at.getTime()) ? at : undefined,
    json: JSON.stringify(rec, null, 2),
  };
}
