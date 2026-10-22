// The shell's navigation: every main entry is in the sidebar, the collapse
// toggle is remembered per device, and the drawer closes on Escape.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '../i18n/setup';
import { makeQueryClient } from './App';
import { mainNav } from './mainNav';
import { routes } from './routes';

function renderSignedIn() {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ authenticated: true, kind: 'user', csrf: 'x' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    ),
  );
  const router = createMemoryRouter(routes, { initialEntries: ['/settings'] });
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

describe('shell navigation', () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('lists every main entry in the navigation', async () => {
    renderSignedIn();
    const nav = await screen.findByRole('navigation', {
      name: i18n.t('common:nav.label'),
    });
    for (const e of mainNav) {
      expect(nav.querySelector(`a[href="${e.to}"]`)?.textContent).toContain(i18n.t(`common:${e.label}`));
    }
  });

  it('remembers the collapsed sidebar', async () => {
    const { unmount } = renderSignedIn();
    fireEvent.click(
      await screen.findByRole('button', {
        name: i18n.t('common:nav.collapse'),
      }),
    );
    expect(document.querySelector('.shell')?.getAttribute('data-collapsed')).toBe('true');
    expect(localStorage.getItem('pcc.sidebar.collapsed')).toBe('1');
    unmount();
    renderSignedIn();
    expect(await screen.findByRole('button', { name: i18n.t('common:nav.expand') })).toBeTruthy();
  });

  it('opens the drawer and closes it on Escape, returning focus', async () => {
    renderSignedIn();
    const menu = await screen.findByRole('button', {
      name: i18n.t('common:nav.menu'),
    });
    fireEvent.click(menu);
    expect(menu.getAttribute('aria-expanded')).toBe('true');
    expect(document.querySelector('main')?.hasAttribute('inert')).toBe(true);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(menu);
  });
});
