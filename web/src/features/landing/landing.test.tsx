// Landing and terms pages: no app data (no API call at all from the landing
// page), the ways in, the terms sections with the #tdm anchor, and the published
// server keys from /.well-known/pcc-keys.json with grouped fingerprints.
import { QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { groupFingerprint, keysPath, toKeys } from './keys';
import LandingPage from './LandingPage';
import TermsPage from './TermsPage';
import AboutPage from './AboutPage';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

const fp = 'ab'.repeat(32);

function renderAt(path: string, element: ReactNode) {
  const router = createMemoryRouter([{ path, element }], { initialEntries: [path] });
  render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

function stubFetch(handler: (path: string) => Response) {
  const seen: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((req: Request) => {
      const path = new URL(req.url).pathname;
      seen.push(path);
      return Promise.resolve(handler(path));
    }),
  );
  return seen;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('keys', () => {
  it('keeps only well-formed keys', () => {
    const keys = toKeys({
      server: [
        { key_id: 'k1', alg: 'ed25519', fingerprint: fp, valid_from: '2026-01-01T00:00:00Z' },
        { key_id: 'k0', alg: 'ed25519', fingerprint: fp, valid_from: '2025-01-01T00:00:00Z', valid_until: '2026-01-01T00:00:00Z' },
        { key_id: 'bad', fingerprint: 'zz', valid_from: '2026-01-01T00:00:00Z' },
        { fingerprint: fp },
      ],
    });
    expect(keys.map((k) => k.keyID)).toEqual(['k1', 'k0']);
    expect(keys[1]?.validUntil?.toISOString()).toBe('2026-01-01T00:00:00.000Z');
    expect(toKeys(null)).toEqual([]);
    expect(toKeys({ server: 'x' })).toEqual([]);
    expect(groupFingerprint('0123456789')).toBe('0123 4567 89');
  });
});

describe('landing page', () => {
  it('shows the ways in and no app data', () => {
    const seen = stubFetch(() => new Response(null, { status: 404 }));
    renderAt('/', <LandingPage />);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe(tr('landing:page.title'));
    expect(screen.getByTestId('landing-login').getAttribute('href')).toBe('/login');
    expect(screen.getByTestId('landing-join').getAttribute('href')).toBe('/join');
    expect(screen.getByTestId('landing-terms').getAttribute('href')).toBe('/terms');
    expect(screen.getByTestId('landing-about').getAttribute('href')).toBe('/about');
    expect(seen).toEqual([]);
  });
});

describe('terms page', () => {
  it('has every section, the TDM anchor and the published key fingerprints', async () => {
    const seen = stubFetch((path) =>
      path === keysPath
        ? new Response(
            JSON.stringify({
              server: [{ key_id: 'k1', alg: 'ed25519', public_key: 'x', fingerprint: fp, valid_from: '2026-01-01T00:00:00Z' }],
            }),
            { status: 200, headers: { 'Content-Type': 'application/json' } },
          )
        : new Response(null, { status: 404 }),
    );
    renderAt('/terms', <TermsPage />);
    for (const s of ['service', 'invites', 'conduct', 'moderation', 'tdm', 'data', 'changes']) {
      expect(screen.getByTestId(`terms-${s}`).id).toBe(s);
    }
    expect(screen.getByTestId('terms-tdm').textContent).toContain(tr('landing:terms.sections.tdm.title'));
    const key = await screen.findByTestId('terms-key');
    expect(within(key).getByTestId('terms-fingerprint').textContent).toBe(groupFingerprint(fp));
    expect(within(key).getByText(tr('landing:keys.id', { id: 'k1', alg: 'ed25519' }))).toBeTruthy();
    expect(seen).toEqual([keysPath]);
  });

  it('links the raw key list when loading fails', async () => {
    stubFetch(() => new Response(null, { status: 500 }));
    renderAt('/terms', <TermsPage />);
    const alert = await screen.findByRole('alert');
    expect(alert.querySelector('a')?.getAttribute('href')).toBe(keysPath);
  });
});

describe('about page', () => {
  it('has every section, a table of contents and no app data', () => {
    const seen = stubFetch(() => new Response(null, { status: 404 }));
    renderAt('/about', <AboutPage />);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe(tr('about:title'));
    for (const id of ['joining', 'handshake', 'connections', 'probation', 'distance', 'privacy', 'pace', 'safety', 'moderation']) {
      const s = screen.getByTestId(`about-${id}`);
      expect(within(s).getByRole('heading', { level: 2 }).textContent).toBe(tr(`about:sections.${id}.title`));
      expect(screen.getByRole('link', { name: tr(`about:sections.${id}.title`) }).getAttribute('href')).toBe(`#${id}`);
    }
    expect(seen).toEqual([]);
  });
});
