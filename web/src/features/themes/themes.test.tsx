// Themes: tokens map onto the app's custom properties and nothing else, the
// accessibility switches win (high contrast drops the colors, a text size the
// font size, the dyslexia font the font), every preset passes the contrast
// checks the server applies, the shell applies the stored theme and clears it
// on sign-out, and the page saves a preset, clears to the default, shows the
// live checks and explains a theme_contrast rejection.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import ThemePage, { themeErrorKey } from './ThemePage';
import { ThemeApplier } from './slots';
import { applyTheme, checks, contrast, presetOf, presets, themeProps, themeVars, type ThemeTokens } from './theme';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

const paper = presets.find((p) => p.id === 'paper')?.tokens as ThemeTokens;
const none = { highContrast: false, textScaled: false, dyslexiaFont: false };

function json(status: number, body: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

interface Server {
  theme: ThemeTokens | null;
  puts: ThemeTokens[];
  deletes: number;
  signedIn: boolean;
  reject?: Response;
}

function stubServer(over: Partial<Server> = {}): Server {
  const srv: Server = { theme: null, puts: [], deletes: 0, signedIn: true, ...over };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname.replace(/^\/api/, '');
      if (path === '/session') {
        return json(200, srv.signedIn ? { authenticated: true, kind: 'user', csrf: 'x', me: { ref: 'r', handle: 'alice', display_name: '', role: 'member', inviter: { kind: 'genesis' }, settings: {} } } : { authenticated: false, kind: 'none' });
      }
      if (path === '/me/theme' && req.method === 'GET') return json(200, srv.theme ? { theme: { tokens: srv.theme, css: '', images: [], updated_at: '2026-10-21T00:00:00Z' } } : {});
      if (path === '/me/theme' && req.method === 'PUT') {
        if (srv.reject) return srv.reject;
        const b = (await req.json()) as { tokens: ThemeTokens };
        srv.puts.push(b.tokens);
        srv.theme = b.tokens;
        return json(200, { theme: { tokens: b.tokens, css: '', images: [], updated_at: '2026-10-21T00:00:00Z' } });
      }
      if (path === '/me/theme' && req.method === 'DELETE') {
        srv.deletes++;
        srv.theme = null;
        return json(204, undefined);
      }
      return json(404, { error: 'not_found' });
    }),
  );
  return srv;
}

function renderWith(node: React.ReactNode) {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  applyTheme(document.documentElement, undefined, none);
  delete document.documentElement.dataset.contrast;
  delete document.documentElement.dataset.textScale;
  delete document.documentElement.dataset.dyslexiaFont;
});

describe('theme tokens', () => {
  it('map onto the custom properties only', () => {
    const v = themeVars(paper, none);
    expect(v.get('--color-integrity-text')).toBe('#4a2a05');
    expect(v.get('--radius')).toBe('4px');
    expect(v.get('--space-4')).toBe('16px');
    expect(v.get('--font-body')).toContain('Georgia');
    expect(v.get('--font-size')).toBe('17px');
    for (const k of v.keys()) expect(themeProps).toContain(k);
  });

  it('yield to the accessibility switches', () => {
    expect([...themeVars(paper, { ...none, highContrast: true }).keys()].some((k) => k.startsWith('--color-'))).toBe(false);
    expect(themeVars(paper, { ...none, textScaled: true }).has('--font-size')).toBe(false);
    expect(themeVars(paper, { ...none, dyslexiaFont: true }).has('--font-body')).toBe(false);
  });

  it('never pass through values outside the schema', () => {
    const bad = { ...paper, colors: { ...paper.colors, bg: 'red;display:none' }, font: 'x' as ThemeTokens['font'], font_size: 8, radius: 99 };
    const v = themeVars(bad, none);
    expect(v.has('--color-bg')).toBe(false);
    expect(v.has('--font-body')).toBe(false);
    expect(v.get('--font-size')).toBe('14px');
    expect(v.get('--radius')).toBe('24px');
  });

  it('every preset passes the checks', () => {
    expect(presets.length).toBeGreaterThanOrEqual(4);
    for (const p of presets) {
      for (const c of checks(p.tokens.colors)) expect(c.ok, `${p.id} ${c.id}`).toBe(true);
      expect(presetOf(p.tokens)).toBe(p.id);
    }
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21);
    expect(checks({ ...paper.colors, integrity_text: '#efe5d0' }).find((c) => c.id === 'integrity')?.ok).toBe(false);
  });

  it('apply to an element and clear', () => {
    const el = document.createElement('div');
    applyTheme(el, paper, none);
    expect(el.style.getPropertyValue('--color-bg')).toBe('#f7f3ea');
    applyTheme(el, undefined, none);
    expect(el.style.getPropertyValue('--color-bg')).toBe('');
  });
});

describe('ThemeApplier', () => {
  it('applies the stored theme and re-applies when high contrast toggles', async () => {
    stubServer({ theme: paper });
    renderWith(<ThemeApplier />);
    const root = document.documentElement;
    await waitFor(() => {
      expect(root.style.getPropertyValue('--color-bg')).toBe('#f7f3ea');
    });
    root.dataset.contrast = 'high';
    await waitFor(() => {
      expect(root.style.getPropertyValue('--color-bg')).toBe('');
    });
    expect(root.style.getPropertyValue('--radius')).toBe('4px');
  });

  it('applies nothing when signed out', async () => {
    const srv = stubServer({ theme: paper, signedIn: false });
    renderWith(<ThemeApplier />);
    await waitFor(() => {
      expect(fetch).toHaveBeenCalled();
    });
    expect(document.documentElement.style.getPropertyValue('--color-bg')).toBe('');
    expect(srv.puts).toEqual([]);
  });
});

describe('ThemePage', () => {
  it('saves a preset and goes back to the default', async () => {
    const srv = stubServer();
    renderWith(<ThemePage />);
    const form = await screen.findByTestId('theme-form');
    expect(form.dataset.choice).toBe('default');
    fireEvent.click(screen.getAllByTestId('theme-preset').find((r) => r.dataset.preset === 'paper') as HTMLElement);
    expect(screen.getByTestId('theme-preview').style.getPropertyValue('--color-bg')).toBe('#f7f3ea');
    fireEvent.click(screen.getByTestId('theme-save'));
    await screen.findByTestId('theme-saved');
    expect(srv.puts).toEqual([paper]);

    fireEvent.click(screen.getAllByTestId('theme-preset').find((r) => r.dataset.preset === 'default') as HTMLElement);
    expect(screen.getByTestId('theme-preview').style.getPropertyValue('--color-bg')).toBe('');
    fireEvent.click(screen.getByTestId('theme-save'));
    await waitFor(() => {
      expect(srv.deletes).toBe(1);
    });
  });

  it('opens on the stored theme, edits become custom with live checks, and explains a rejection', async () => {
    stubServer({
      theme: paper,
      reject: json(422, { error: 'theme_contrast', field: 'colors.integrity_text' }),
    });
    renderWith(<ThemePage />);
    const form = await screen.findByTestId('theme-form');
    await waitFor(() => {
      expect(form.dataset.choice).toBe('paper');
    });
    fireEvent.change(screen.getByTestId('theme-color-integrity_text'), { target: { value: '#e0d5c0' } });
    expect(form.dataset.choice).toBe('custom');
    const integrity = screen.getAllByTestId('theme-check').find((c) => c.dataset.check === 'integrity');
    expect(integrity?.dataset.ok).toBe('false');
    fireEvent.click(screen.getByTestId('theme-save'));
    const err = await screen.findByTestId('theme-error');
    expect(err.dataset.code).toBe('theme_contrast');
    expect(err.textContent).toBe(tr('themes:errors.theme_contrast', { check: 'integrity' }));
    expect(integrity?.dataset.failed).toBe('true');
  });

  it('maps error codes', () => {
    expect(themeErrorKey(new ApiError(422, { error: 'theme_font' }))).toBe('themes:errors.theme_font');
    expect(themeErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
  });
});
