// Settings feature: pure helpers and the pages against a stubbed API.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import DeletePage from './DeletePage';
import DisplayPage from './DisplayPage';
import ExportPage from './ExportPage';
import LanguagesPage from './LanguagesPage';
import PrivacyPage from './PrivacyPage';
import { RecoveryBanner } from './slots';
import {
  accountErrorKey,
  canonicalTag,
  exportFileName,
  fromUIPrefs,
  handleMatches,
  toA11y,
  toUIPrefs,
  translateOptions,
} from './vm';

interface Call {
  method: string;
  path: string;
  body: unknown;
}

type Handler = (c: Call) => Response | undefined;

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const me = {
  ref: 'u1',
  handle: 'alice',
  display_name: 'Alice',
  role: 'member',
  inviter: { kind: 'genesis' },
  settings: {
    ui_locale: 'en',
    languages: ['en', 'fr'],
    autotranslate: false,
    rank_preset: 'balanced',
    name_in_chains: false,
    suggestible: true,
    named_in_likes: true,
    slippery_default: false,
    ui_prefs: {},
  },
};

// stubAPI answers /api/session with a signed-in alice and routes everything
// else through handler; it records every call.
function stubAPI(handler: Handler, meOver: Partial<typeof me> = {}): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      const path = url.pathname.replace(/^\/api/, '');
      const text = await req.text();
      const c: Call = { method: req.method, path, body: text ? (JSON.parse(text) as unknown) : undefined };
      calls.push(c);
      if (path === '/session') return json(200, { authenticated: true, kind: 'user', csrf: 'x', me: { ...me, ...meOver } });
      if (path === '/me' && req.method === 'GET') return json(200, { ...me, ...meOver });
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderPage(el: ReactNode) {
  const router = createMemoryRouter(
    [
      { path: '/settings/x', element: el },
      { path: '/', element: <p>home</p> },
    ],
    { initialEntries: ['/settings/x'] },
  );
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('settings view-models', () => {
  it('reads ui_prefs with defaults and writes the server schema', () => {
    const p = toUIPrefs({ format: 'compact', a11y: { text_scale: 'large', reduced_motion: true, bogus: 1 }, extra: 1 });
    expect(p).toEqual({
      format: 'compact',
      density: 'comfortable',
      a11y: { contrast: 'normal', textScale: 'large', reducedMotion: true, dyslexiaFont: false },
    });
    expect(fromUIPrefs(p)).toEqual({
      format: 'compact',
      density: 'comfortable',
      a11y: { contrast: 'normal', text_scale: 'large', reduced_motion: true, dyslexia_font: false },
    });
    expect(toUIPrefs({ format: 'explorer' }).format).toBe('explorer');
    expect(toUIPrefs({ format: 'grid' }).format).toBe('cards');
    expect(toUIPrefs(null).a11y.textScale).toBe('normal');
    expect(toA11y(p)).toEqual({ contrast: 'normal', textScale: 'large', reducedMotion: true, dyslexiaFont: false });
  });

  it('canonicalizes language tags and limits translation targets to them', () => {
    expect(canonicalTag(' pt-br ')).toBe('pt-BR');
    expect(canonicalTag('und')).toBeNull();
    expect(canonicalTag('not a tag')).toBeNull();
    expect(translateOptions(['en', 'fr-ca', 'EN', 'zz-!!'])).toEqual(['en', 'fr-CA']);
  });

  it('matches handles like the server and names errors', () => {
    expect(handleMatches(' @Alice ', 'alice')).toBe(true);
    expect(handleMatches('alic', 'alice')).toBe(false);
    expect(accountErrorKey(new ApiError(422, { error: 'translate_to_not_in_languages' }))).toBe(
      'account:errors.translate_to_not_in_languages',
    );
    expect(accountErrorKey(new ApiError(401, { error: 'step_up_required' }))).toBe('errors:step_up_required');
    expect(exportFileName(new Date('2026-03-04T05:06:07Z'))).toBe('pcc-export-2026-03-04.zip');
  });
});

describe('settings pages', () => {
  it('saves display settings and applies the accessibility switches', async () => {
    const calls = stubAPI((c) => (c.path === '/me/settings' ? json(200, me.settings) : undefined));
    renderPage(<DisplayPage />);
    fireEvent.click(await screen.findByLabelText(i18n.t('settings:display.format.compact')));
    fireEvent.change(screen.getByLabelText(i18n.t('settings:display.a11y.textScale')), { target: { value: 'xlarge' } });
    fireEvent.click(screen.getByLabelText(i18n.t('settings:display.preset.latest')));
    fireEvent.change(screen.getByLabelText(i18n.t('settings:display.name.label')), { target: { value: 'Ally' } });
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:form.save') }));
    expect(await screen.findByText(i18n.t('settings:form.saved'))).toBeTruthy();
    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.body).toEqual({
      display_name: 'Ally',
      rank_preset: 'latest',
      ui_prefs: {
        format: 'compact',
        density: 'comfortable',
        a11y: { contrast: 'normal', text_scale: 'xlarge', reduced_motion: false, dyslexia_font: false },
      },
    });
    expect(document.documentElement.dataset.textScale).toBe('xlarge');
  });

  it('offers only the user languages as translation targets and shows server problems', async () => {
    const calls = stubAPI((c) =>
      c.path === '/me/settings' ? json(422, { error: 'translate_to_not_in_languages', field: 'translate_to' }) : undefined,
    );
    renderPage(<LanguagesPage />);
    const select = await screen.findByLabelText(i18n.t('settings:languages.translateTo.label'));
    const values = Array.from((select as HTMLSelectElement).options).map((o) => o.value);
    expect(values).toEqual(['', 'en', 'fr']);
    fireEvent.change(select, { target: { value: 'fr' } });
    fireEvent.change(screen.getByLabelText(i18n.t('settings:languages.list.newLabel')), { target: { value: 'de-de' } });
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:languages.list.add') }));
    expect(screen.getByText('(de-DE)')).toBeTruthy();
    // Removing the target language clears the target.
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:languages.list.remove', { lang: 'fr' }) }));
    expect((select as HTMLSelectElement).value).toBe('');
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:form.save') }));
    expect(await screen.findByText(i18n.t('account:errors.translate_to_not_in_languages'))).toBeTruthy();
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({
      ui_locale: 'en',
      languages: ['en', 'de-DE'],
      translate_to: '',
      autotranslate: false,
    });
  });

  it('saves privacy toggles', async () => {
    const calls = stubAPI((c) => (c.path === '/me/settings' ? json(200, me.settings) : undefined));
    renderPage(<PrivacyPage />);
    fireEvent.click(await screen.findByLabelText(i18n.t('settings:privacy.nameInChains.label')));
    fireEvent.click(screen.getByLabelText(i18n.t('settings:privacy.suggestible.label')));
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:form.save') }));
    await screen.findByText(i18n.t('settings:form.saved'));
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({
      name_in_chains: true,
      suggestible: false,
      named_in_likes: true,
      slippery_default: false,
    });
  });

  it('asks for a step-up when the export is not fresh, then downloads', async () => {
    let fresh = false;
    stubAPI((c) => {
      if (c.path !== '/me/export') return undefined;
      if (!fresh) return json(401, { error: 'step_up_required' });
      return new Response('PK', { status: 200, headers: { 'Content-Type': 'application/zip' } });
    });
    const created = vi.fn(() => 'blob:x');
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: created, revokeObjectURL: vi.fn() }));
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    renderPage(<ExportPage />);
    fireEvent.click(await screen.findByRole('button', { name: i18n.t('settings:export.button') }));
    expect(await screen.findByText(i18n.t('errors:step_up_required'))).toBeTruthy();
    fresh = true;
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:export.button') }));
    expect(await screen.findByText(i18n.t('settings:export.done'))).toBeTruthy();
    expect(created).toHaveBeenCalledOnce();
    expect(click).toHaveBeenCalledOnce();
  });

  it('deletes only after the handle is typed, and surfaces step-up', async () => {
    let fresh = false;
    const calls = stubAPI((c) => {
      if (c.path !== '/me/delete') return undefined;
      return fresh ? new Response(null, { status: 202 }) : json(401, { error: 'step_up_required' });
    });
    renderPage(<DeletePage />);
    const button = await screen.findByRole('button', { name: i18n.t('settings:delete.button') });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    const input = screen.getByLabelText(i18n.t('settings:delete.confirmLabel', { handle: 'alice' }));
    fireEvent.change(input, { target: { value: 'alic' } });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(input, { target: { value: '@Alice' } });
    expect((button as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(button);
    expect(await screen.findByText(i18n.t('errors:step_up_required'))).toBeTruthy();
    fresh = true;
    fireEvent.click(button);
    expect(await screen.findByText('home')).toBeTruthy();
    expect(calls.filter((c) => c.path === '/me/delete').map((c) => c.body)).toEqual([
      { confirm_handle: '@Alice' },
      { confirm_handle: '@Alice' },
    ]);
  });
});

describe('recovery banner', () => {
  it('shows a pending recovery and cancels it', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/me/recovery') return json(200, { pending_until: '2026-01-02T00:00:00Z' });
      if (c.path === '/me/recovery/cancel') return new Response(null, { status: 204 });
      return undefined;
    });
    renderPage(<RecoveryBanner />);
    const cancel = await screen.findByRole('button', { name: i18n.t('account:banner.cancel') });
    expect(screen.getByRole('alert').textContent).toContain('2026');
    fireEvent.click(cancel);
    expect(await screen.findByText(i18n.t('account:banner.cancelled'))).toBeTruthy();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/me/recovery/cancel')).toBe(true);
  });

  it('renders nothing without a pending recovery', async () => {
    const calls = stubAPI((c) => (c.path === '/me/recovery' ? json(200, {}) : undefined));
    const { container } = renderPage(<RecoveryBanner />);
    await waitFor(() => {
      expect(calls.some((c) => c.path === '/me/recovery')).toBe(true);
    });
    expect(container.textContent).toBe('');
  });
});
