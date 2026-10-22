// The shell's navigation: every main entry is in the sidebar, the collapse
// toggle is remembered per device, the phone tab bar holds the main pages and
// the menu, and the drawer closes on Escape.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '../i18n/setup';
import { makeQueryClient } from './App';
import { mainNav, moreNav, tabNav } from './mainNav';
import { routes } from './routes';

function renderSignedIn(role?: string, authenticated = true) {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify(
            authenticated ? { authenticated: true, kind: 'user', csrf: 'x', me: role ? { role } : undefined } : { authenticated: false, kind: 'none' },
          ),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
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
    for (const e of mainNav.filter((n) => n.who === undefined)) {
      expect(nav.querySelector(`a[href="${e.to}"]`)?.textContent).toContain(i18n.t(`common:${e.label}`));
    }
  });

  function hrefs(el: Element | null | undefined): (string | null)[] {
    return [...(el?.querySelectorAll('a') ?? [])].map((a) => a.getAttribute('href'));
  }

  it('puts the terms and the moderation pages under the main entries, by role', async () => {
    const { unmount } = renderSignedIn();
    const nav = await screen.findByRole('navigation', { name: i18n.t('common:nav.label') });
    expect(hrefs(nav.querySelector('.nav-more'))).toEqual(['/about', '/terms', '/mod/log', '/blame']);
    expect(hrefs(nav)).not.toContain('/mod');
    unmount();
    renderSignedIn('admin');
    const admin = await screen.findByRole('navigation', { name: i18n.t('common:nav.label') });
    await vi.waitFor(() => {
      expect(hrefs(admin.querySelector('.nav-more'))).toEqual(moreNav.map((e) => e.to));
    });
    expect(hrefs(admin)).toContain('/mod');
  });

  it('shows only the about page and the terms, in the footer, when signed out', async () => {
    renderSignedIn(undefined, false);
    const footer = await screen.findByTestId('site-footer');
    expect(hrefs(footer)).toEqual(['/about', '/terms']);
    expect(screen.queryByRole('navigation', { name: i18n.t('common:nav.label') })).toBeNull();
  });

  it('puts the main pages and the menu in the phone tab bar', async () => {
    renderSignedIn();
    const tabs = await screen.findByRole('navigation', { name: i18n.t('common:nav.tabs') });
    expect(hrefs(tabs)).toEqual(tabNav.map((e) => e.to));
    expect(tabs.querySelector('button[aria-controls]')?.textContent).toContain(i18n.t('common:nav.menu'));
  });

  it('has no tab bar when signed out', async () => {
    renderSignedIn(undefined, false);
    await screen.findByTestId('site-footer');
    expect(screen.queryByRole('navigation', { name: i18n.t('common:nav.tabs') })).toBeNull();
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
    expect(document.querySelector('.shell-body')?.hasAttribute('inert')).toBe(true);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(menu);
  });
});
