// Notification view-models: the API's NotificationView mapped once. Owner:
// P0-W10 → P1-W05. Data values are codes and numbers only (never prose).
import type { components } from '../../api/schema.gen';
import { asRef, type HopVM, type Ref } from '../../vm/types';

type S = components['schemas'];

export type NotificationKind = S['NotificationView']['kind'];

export interface NotificationVM {
  id: Ref;
  kind: NotificationKind;
  count: number;
  person?: HopVM;
  post?: Ref;
  data: Record<string, string | number | boolean>;
  at: Date;
  read: boolean;
}

export interface NotificationPage {
  items: NotificationVM[];
  next?: string;
  unread: number;
}

function codes(d: Record<string, unknown> | undefined): NotificationVM['data'] {
  const out: NotificationVM['data'] = {};
  for (const [k, v] of Object.entries(d ?? {})) {
    if ((typeof v === 'string' && /^[a-z][a-z0-9_]*$/.test(v)) || typeof v === 'number' || typeof v === 'boolean') out[k] = v;
  }
  return out;
}

export function toNotificationVM(n: S['NotificationView']): NotificationVM {
  return {
    id: asRef(n.id),
    kind: n.kind,
    count: n.count,
    ...(n.person ? { person: { ref: asRef(n.person.ref), handle: n.person.handle } } : {}),
    ...(n.post ? { post: asRef(n.post) } : {}),
    data: codes(n.data),
    at: new Date(n.at),
    read: n.read,
  };
}

export function toNotificationPage(p: S['NotifyListOut']): NotificationPage {
  return { items: p.items.map(toNotificationVM), unread: p.unread, ...(p.next ? { next: p.next } : {}) };
}

// messageArgs are the ICU arguments of a notification's catalog message.
export function messageArgs(n: NotificationVM): Record<string, string | number> {
  const str = (k: string) => {
    const v = n.data[k];
    return typeof v === 'string' ? v : 'other';
  };
  return {
    who: n.person ? 'person' : 'none',
    count: n.count,
    others: Math.max(0, n.count - 1),
    reason: str('reason'),
    action: str('action'),
  };
}
