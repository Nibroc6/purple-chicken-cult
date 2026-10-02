// WebAuthn virtual authenticator over CDP (Chromium): passkey ceremonies run
// without hardware in e2e tests (plan §7).
import type { BrowserContext, Page } from '@playwright/test';

export interface VirtualAuthenticator {
  id: string;
  remove(): Promise<void>;
}

export async function addVirtualAuthenticator(context: BrowserContext, page: Page): Promise<VirtualAuthenticator> {
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
    },
  });
  return {
    id: authenticatorId,
    remove: async () => {
      await cdp.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId });
    },
  };
}
