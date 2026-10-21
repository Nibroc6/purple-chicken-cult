// View-models of the recovery feature and the recovering device's ticket
// store. Owner: P1-W10.
import type { components } from '../../api/schema.gen';

type StatusView = components['schemas']['AccountRecoveryStatus'];
type RequestView = components['schemas']['AccountGuardianRequest'];

export type RecoveryState = StatusView['state'];

export interface RecoveryStatusVM {
  state: RecoveryState;
  releaseAt?: Date;
  enrollURL?: string;
}

export function toStatusVM(v: StatusView): RecoveryStatusVM {
  return {
    state: v.state,
    ...(v.release_at ? { releaseAt: new Date(v.release_at) } : {}),
    ...(v.enroll_url ? { enrollURL: v.enroll_url } : {}),
  };
}

export interface GuardianRequestVM {
  id: string;
  handle: string;
  displayName: string;
  expiresAt: Date;
}

export function toGuardianRequestVM(v: RequestView): GuardianRequestVM {
  return { id: v.id, handle: v.account.handle, displayName: v.account.display_name, expiresAt: new Date(v.expires_at) };
}

// pollEvery is how often the recovering device asks for its status: often
// while the guardians act, rarely during the 24 h hold, never once released
// (every ask issues a new enrollment link) or finished.
export function pollEvery(state: RecoveryState | undefined): number | false {
  switch (state) {
    case undefined:
    case 'claimed':
    case 'approving':
      return 5_000;
    case 'holding':
      return 60_000;
    default:
      return false;
  }
}

// The ticket outlives the device's short guest session (the hold lasts a
// day), so it is kept in this browser until the recovery ends.
const TICKET_KEY = 'pcc.recovery.ticket';

export function loadTicket(): string | undefined {
  try {
    return localStorage.getItem(TICKET_KEY) ?? undefined;
  } catch {
    return undefined;
  }
}

export function saveTicket(ticket: string): void {
  try {
    localStorage.setItem(TICKET_KEY, ticket);
  } catch {
    // Private mode: the ticket lives as long as the page.
  }
}

export function clearTicket(): void {
  try {
    localStorage.removeItem(TICKET_KEY);
  } catch {
    // Nothing stored.
  }
}
