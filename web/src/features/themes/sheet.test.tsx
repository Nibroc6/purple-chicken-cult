// Theme CSS (raw, the reader's own): image(name) becomes a signed URL and
// nothing else gets into url(), the preview nests the sheet under the preview
// canvas, locked routes are known, and the page warns before editing, checks
// image names live, saves only the images the CSS uses, and explains a server
// rejection.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import ThemePage from './ThemePage';
import { buildSheet, checkSheet, isLockedRoute, sheetImages } from './sheet';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

describe('buildSheet', () => {
  const urls = new Map([
    ['heart', 'https://media.pcc.example/k1?sig=abc'],
    ['bad', 'https://evil.example/") ; } * { display: none'],
  ]);

  it('swaps image(name) for its signed URL, and only safe URLs', () => {
    const css = '[data-part=like] { background: image( heart ) center; }\n.brand { background: image(bad) } .x { background: image(none-yet) }';
    expect(buildSheet(css, urls, 'app')).toBe(
      '[data-part=like] { background: url("https://media.pcc.example/k1?sig=abc") center; }\n.brand { background: none } .x { background: none }',
    );
  });

  it('nests the preview under the preview canvas', () => {
    expect(buildSheet('.post { color: red }', urls, 'preview')).toBe('[data-theme-preview] {\n.post { color: red }\n}');
  });
});

it('checks image names and size like the server', () => {
  const images = new Set(['heart']);
  expect(checkSheet('.post { display: none }', images)).toBeUndefined();
  expect(checkSheet('a {}\r\nb { background: image(nope) }', images)).toEqual({ line: 2 });
  expect(checkSheet(' '.repeat(32 * 1024 + 1), images)).toEqual({ line: 0 });
  expect([...sheetImages('a { background: image(heart) } b { background: image(logo-2) }')]).toEqual(['heart', 'logo-2']);
});

it('knows the locked routes', () => {
  for (const p of ['/login', '/enroll', '/join', '/recover', '/settings', '/settings/theme', '/settings/passkeys', '/mod/recovery', '/cosign/check', '/report']) {
    expect(isLockedRoute(p)).toBe(true);
  }
  for (const p of ['/', '/compose', '/p/abc', '/friends', '/settingsx', '/people/x']) {
    expect(isLockedRoute(p)).toBe(false);
  }
  // The report form as a modal over any page; other modals (likes) are not locked.
  expect(isLockedRoute('/p/abc', '?report=post:abc')).toBe(true);
  expect(isLockedRoute('/', '?x=1&report=user:u1')).toBe(true);
  expect(isLockedRoute('/p/abc', '?likes=abc')).toBe(false);
});

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const paper = { bg: '#f7f3ea', surface: '#fffdf8', text: '#2b2620', muted: '#5f574b', accent: '#8a3b12', border: '#e4dccb', integrity_bg: '#efe5d0', integrity_text: '#3a2a10' };

interface Put {
  tokens: unknown;
  css?: string;
  images?: { name: string; ref: string }[];
}

function stub(reject?: Response, css = '[data-part=like] { background: image(heart) }\n') {
  const puts: Put[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname.replace(/^\/api/, '');
      if (path === '/session') {
        return json(200, { authenticated: true, kind: 'user', csrf: 'x', me: { ref: 'r', handle: 'alice', display_name: '', role: 'member', inviter: { kind: 'genesis' }, settings: {} } });
      }
      if (path === '/me/theme' && req.method === 'GET') {
        return json(200, {
          theme: { tokens: { colors: paper }, css, images: [{ name: 'heart', ref: 'h1', w: 32, h: 32 }, { name: 'old', ref: 'o1', w: 8, h: 8 }], updated_at: '2026-10-21T00:00:00Z' },
        });
      }
      if (path === '/media/h1/url' || path === '/media/o1/url') return json(200, { url: 'https://media.pcc.example/x', expires_at: '2099-01-01T00:00:00Z' });
      if (path === '/me/theme' && req.method === 'PUT') {
        if (reject) return reject;
        const b = (await req.json()) as Put;
        puts.push(b);
        return json(200, { theme: { tokens: b.tokens, css: b.css ?? '', images: [], updated_at: '2026-10-21T00:00:00Z' } });
      }
      return json(404, { error: 'not_found' });
    }),
  );
  return puts;
}

function renderPage() {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <MemoryRouter>
        <ThemePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('ThemePage advanced mode', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('warns before editing, checks image names live, and saves only the images the CSS uses', async () => {
    const puts = stub();
    renderPage();
    const box = await screen.findByTestId<HTMLTextAreaElement>('theme-css');
    expect(box.value).toBe('[data-part=like] { background: image(heart) }\n');
    expect(screen.getByTestId('theme-css-warning').textContent).toBe(tr('themes:advanced.warning'));
    // A theme that already has CSS was acknowledged before.
    expect(box.disabled).toBe(false);
    expect(screen.getAllByTestId('theme-image').map((e) => e.dataset.name)).toEqual(['heart', 'old']);

    fireEvent.change(box, { target: { value: '.post { display: none }\n.brand { background: image(nope) }' } });
    expect(screen.getByTestId('theme-css-status').textContent).toContain(tr('themes:advanced.errors.theme_css', { line: 2 }));
    expect(screen.getByTestId<HTMLButtonElement>('theme-save').disabled).toBe(true);

    fireEvent.change(box, { target: { value: '.post { display: none }\n[data-part=like] { background: image(heart) }' } });
    expect(screen.getByTestId('theme-css-status').textContent).toBe('');
    fireEvent.click(screen.getByTestId('theme-save'));
    await screen.findByTestId('theme-saved');
    expect(puts[0]?.css).toBe('.post { display: none }\n[data-part=like] { background: image(heart) }');
    expect(puts[0]?.images).toEqual([{ name: 'heart', ref: 'h1' }]);
  });

  it('keeps the editor locked until the risks are acknowledged', async () => {
    stub(undefined, '');
    renderPage();
    const box = await screen.findByTestId<HTMLTextAreaElement>('theme-css');
    expect(box.disabled).toBe(true);
    fireEvent.click(screen.getByTestId('theme-css-ack'));
    expect(box.disabled).toBe(false);
  });

  it('explains a theme_css rejection by line', async () => {
    stub(json(422, { error: 'theme_css', field: 'css.3' }));
    renderPage();
    await screen.findByTestId('theme-css');
    fireEvent.click(screen.getByTestId('theme-save'));
    await waitFor(() => {
      expect(screen.getByTestId('theme-error').textContent).toContain(tr('themes:advanced.errors.theme_css', { line: 3 }));
    });
  });

  it('checks image names before uploading', async () => {
    stub();
    renderPage();
    await screen.findByTestId('theme-css');
    fireEvent.change(screen.getByTestId('theme-image-name'), { target: { value: 'heart' } });
    fireEvent.click(screen.getByTestId('theme-image-add'));
    expect((await screen.findByTestId('theme-image-error')).textContent).toContain(tr('themes:advanced.imageNameTaken'));
    fireEvent.change(screen.getByTestId('theme-image-name'), { target: { value: '9lives' } });
    fireEvent.click(screen.getByTestId('theme-image-add'));
    expect((await screen.findByTestId('theme-image-error')).textContent).toContain(tr('themes:advanced.imageNameBad'));
  });
});
