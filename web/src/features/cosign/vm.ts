// View-model of a record being co-signed (plan §6.1). Owner: P1-W01.
//
// The dialog shows the record exactly as the server drafted it: every field
// of the canonical JSON except the envelope members ("kind",
// "schema_version") and the co-sign member ("cosign": nonce and issue time),
// which are shown separately. Nothing is hidden or reformatted beyond that, so
// what the person approves is what their passkey signs.

export const recordKinds = [
  'invite',
  'friendship',
  'revocation',
  'post',
  'tombstone',
  'block',
  'report',
  'passkey_change',
  'mod_action',
] as const;
export type RecordKind = (typeof recordKinds)[number];

export const knownFields = [
  'a',
  'b',
  'at',
  'by',
  'blocked',
  'blocker',
  'created_at',
  'credential_id',
  'edge',
  'handshake',
  'invitee',
  'invitee_credential',
  'inviter',
  'issued_at',
  'level',
  'op',
  'user',
] as const;
export type KnownField = (typeof knownFields)[number];

export interface FieldVM {
  name: string;
  known?: KnownField;
  value: string;
}

export interface RecordVM {
  kind?: RecordKind;
  rawKind: string;
  fields: FieldVM[];
  nonce?: string;
  issuedAt?: Date;
  exact: string;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function show(v: unknown): string {
  return typeof v === 'string' ? v : JSON.stringify(v);
}

// toRecordVM maps one drafted record. A value that is not an object is not a
// record and is refused.
export function toRecordVM(raw: unknown): RecordVM {
  if (!isObject(raw)) throw new Error('cosign: not a record');
  const rawKind = typeof raw.kind === 'string' ? raw.kind : '';
  const kind = (recordKinds as readonly string[]).includes(rawKind) ? (rawKind as RecordKind) : undefined;
  const member = isObject(raw.cosign) ? raw.cosign : {};
  const fields: FieldVM[] = Object.keys(raw)
    .filter((k) => k !== 'kind' && k !== 'schema_version' && k !== 'cosign')
    .sort()
    .map((name) => ({
      name,
      known: (knownFields as readonly string[]).includes(name) ? (name as KnownField) : undefined,
      value: show(raw[name]),
    }));
  const issued = typeof member.issued_at === 'string' ? new Date(member.issued_at) : undefined;
  return {
    kind,
    rawKind,
    fields,
    nonce: typeof member.nonce === 'string' ? member.nonce : undefined,
    issuedAt: issued && !Number.isNaN(issued.getTime()) ? issued : undefined,
    exact: JSON.stringify(raw, null, 2),
  };
}

// toRecordVMs maps an intent's record: one record, or the array of drafts of
// the signup ceremony.
export function toRecordVMs(raw: unknown): RecordVM[] {
  return Array.isArray(raw) ? raw.map(toRecordVM) : [toRecordVM(raw)];
}
