// Browser side of the passkey ceremonies (plan §6.4, §17.2). The server sends
// {"publicKey": {...}, "mediation"?} option objects; @simplewebauthn/browser
// turns them into navigator.credentials calls and the answers back into JSON.
// Only one ceremony runs at a time: starting one aborts the previous (e.g. the
// conditional-mediation request of the login page). Owner: P0-W01 → P1-W07.
import {
  WebAuthnAbortService,
  browserSupportsWebAuthn,
  browserSupportsWebAuthnAutofill,
  startAuthentication,
  startRegistration,
  type PublicKeyCredentialCreationOptionsJSON,
  type PublicKeyCredentialRequestOptionsJSON,
} from '@simplewebauthn/browser';

// PasskeyErrorCode is why a ceremony did not produce a credential.
// aborted: superseded by another ceremony (never shown);
// cancelled: the person closed the prompt or it timed out;
// exists: this authenticator already holds a passkey for the account;
// unsupported: no WebAuthn in this browser; failed: anything else.
export type PasskeyErrorCode = 'aborted' | 'cancelled' | 'exists' | 'unsupported' | 'failed';

export class PasskeyError extends Error {
  readonly code: PasskeyErrorCode;

  constructor(code: PasskeyErrorCode) {
    super(code);
    this.name = 'PasskeyError';
    this.code = code;
  }
}

function publicKey(options: unknown): Record<string, unknown> {
  if (typeof options === 'object' && options !== null) {
    const pk = (options as { publicKey?: unknown }).publicKey;
    if (typeof pk === 'object' && pk !== null && typeof (pk as { challenge?: unknown }).challenge === 'string') {
      return pk as Record<string, unknown>;
    }
  }
  throw new PasskeyError('failed');
}

function classify(err: unknown): PasskeyError {
  if (err instanceof PasskeyError) return err;
  const e = err as { name?: unknown; code?: unknown };
  if (e.code === 'ERROR_CEREMONY_ABORTED' || e.name === 'AbortError') return new PasskeyError('aborted');
  if (e.code === 'ERROR_AUTHENTICATOR_PREVIOUSLY_REGISTERED' || e.name === 'InvalidStateError') {
    return new PasskeyError('exists');
  }
  if (e.name === 'NotAllowedError') return new PasskeyError('cancelled');
  return new PasskeyError('failed');
}

export function passkeysSupported(): boolean {
  return browserSupportsWebAuthn();
}

// autofillSupported reports whether conditional mediation (passkey autofill) works here.
export async function autofillSupported(): Promise<boolean> {
  try {
    return await browserSupportsWebAuthnAutofill();
  } catch {
    return false;
  }
}

// getAssertion answers request options with a passkey assertion. With autofill
// the request waits for the person to pick a passkey from the autofill list of
// an <input autocomplete="username webauthn">.
export async function getAssertion(options: unknown, autofill = false): Promise<Record<string, unknown>> {
  if (!passkeysSupported()) throw new PasskeyError('unsupported');
  const optionsJSON = publicKey(options) as unknown as PublicKeyCredentialRequestOptionsJSON;
  try {
    const res = await startAuthentication({ optionsJSON, useBrowserAutofill: autofill });
    return res as unknown as Record<string, unknown>;
  } catch (err) {
    throw classify(err);
  }
}

// createPasskey answers creation options with a new passkey.
export async function createPasskey(options: unknown): Promise<Record<string, unknown>> {
  if (!passkeysSupported()) throw new PasskeyError('unsupported');
  const optionsJSON = publicKey(options) as unknown as PublicKeyCredentialCreationOptionsJSON;
  try {
    const res = await startRegistration({ optionsJSON });
    return res as unknown as Record<string, unknown>;
  } catch (err) {
    throw classify(err);
  }
}

// cancelCeremony aborts a pending ceremony (e.g. the autofill request on unmount).
export function cancelCeremony(): void {
  WebAuthnAbortService.cancelCeremony();
}
