// Theme CSS: the client parser agrees with the server on every shared vector,
// the stylesheet only ever uses first-party selectors that skip integrity
// elements and locked dialogs, images become signed URLs (nothing else gets
// into url()), locked routes are known, and the page checks theme CSS live,
// saves it with only the images it uses, and explains a server rejection.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import ThemePage from './ThemePage';
import { buildRules, checkSheet, formatSheet, isLockedRoute, parseSheet, type Rule } from './sheet';
import vectors from './sheet.vectors.json';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

interface Vector {
  name: string;
  css: string;
  out?: string;
  error?: string;
  field?: string;
}

const images = new Set(vectors.images);

function rulesOf(css: string): Rule[] {
  const p = parseSheet(css, images);
  if (!p.ok) throw new Error(`does not parse: ${css}`);
  return p.rules;
}

describe('theme CSS vectors (shared with gate/themes)', () => {
  for (const v of vectors.cases as Vector[]) {
    it(v.name, () => {
      const p = parseSheet(v.css, images);
      const err = p.ok ? checkSheet(p.rules, vectors.colors) : p.error;
      if (v.error) {
        expect(err).toEqual({ code: v.error, line: Number(v.field?.replace('css.', '')) });
        return;
      }
      expect(err).toBeUndefined();
      if (!p.ok) return;
      expect(formatSheet(p.rules)).toBe(v.out);
      expect(formatSheet(rulesOf(v.out ?? ''))).toBe(v.out);
    });
  }
});

describe('buildRules', () => {
  const urls = new Map([
    ['heart', 'https://media.pcc.example/k1?sig=abc'],
    ['logo', 'https://evil.example/") ; } * { display: none'],
  ]);

  it('maps parts onto first-party selectors that skip integrity elements and dialogs', () => {
    const [rule] = buildRules(rulesOf('like:pressed { background-image: image(heart); label: hidden }'), urls, 'app');
    expect(rule).toBe(
      '@media not (forced-colors: active) { .shell [data-part="like"]:not(.integrity, .integrity *, .stepup-backdrop *, .cosign-backdrop *, .auth-second *)[aria-pressed="true"] { ' +
        'background-image: url("https://media.pcc.example/k1?sig=abc"); background-repeat: no-repeat; background-position: center; background-size: contain; font-size: 0; } }',
    );
  });

  it('scopes the preview to the preview canvas, and the page part to the root', () => {
    const out = buildRules(rulesOf('page { background-color: #ffffff }\ninput { border-width: 2px }'), urls, 'preview');
    expect(out[0]).toContain('[data-theme-preview] { background-color: #ffffff; }');
    expect(out[1]).toContain('[data-theme-preview] input:not(');
    expect(out[1]).toContain('[data-theme-preview] textarea:not(');
    expect(out[1]).toContain('[data-theme-preview] select:not(');
  });

  it('puts only safe URLs in url(), and drops an image without one', () => {
    const out = buildRules(rulesOf('brand { background-image: image(logo); label: hidden }\nsave { background-image: image(paper); border-width: 1px }'), urls, 'app');
    expect(out).toHaveLength(2);
    expect(out[0]).not.toContain('url(');
    expect(out[0]).toContain('font-size: 0;');
    expect(out[1]).not.toContain('url(');
    expect(out[1]).toContain('border-width: 1px;');
  });
});

it('knows the locked routes', () => {
  for (const p of ['/login', '/enroll', '/join', '/recover', '/settings', '/settings/theme', '/settings/passkeys', '/mod/recovery', '/cosign/check', '/report']) {
    expect(isLockedRoute(p)).toBe(true);
  }
  for (const p of ['/', '/compose', '/p/abc', '/friends', '/settingsx', '/people/x']) {
    expect(isLockedRoute(p)).toBe(false);
  }
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

function stub(reject?: Response) {
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
          theme: { tokens: { colors: paper }, css: 'like {\n  background-image: image(heart);\n  label: hidden;\n}\n', images: [{ name: 'heart', ref: 'h1', w: 32, h: 32 }, { name: 'old', ref: 'o1', w: 8, h: 8 }], updated_at: '2026-10-21T00:00:00Z' },
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

  it('checks theme CSS live and saves it with only the images it uses', async () => {
    const puts = stub();
    renderPage();
    const box = await screen.findByTestId('theme-css');
    expect((box as HTMLTextAreaElement).value).toBe('like {\n  background-image: image(heart);\n  label: hidden;\n}\n');
    expect(screen.getAllByTestId('theme-image').map((e) => e.dataset.name)).toEqual(['heart', 'old']);

    fireEvent.change(box, { target: { value: 'card {\n  display: none;\n}' } });
    expect(screen.getByTestId('theme-css-status').textContent).toContain(tr('themes:advanced.errors.theme_css', { line: 2 }));
    expect(screen.getByTestId<HTMLButtonElement>('theme-save').disabled).toBe(true);

    fireEvent.change(box, { target: { value: 'card { color: #eeeeee }' } });
    expect(screen.getByTestId('theme-css-status').textContent).toContain(tr('themes:advanced.errors.theme_contrast', { line: 1 }));

    fireEvent.change(box, { target: { value: 'like { background-image: image(heart); label: hidden }' } });
    expect(screen.getByTestId('theme-css-status').textContent).toContain(tr('themes:advanced.ok'));
    fireEvent.click(screen.getByTestId('theme-save'));
    await screen.findByTestId('theme-saved');
    expect(puts[0]?.css).toBe('like { background-image: image(heart); label: hidden }');
    expect(puts[0]?.images).toEqual([{ name: 'heart', ref: 'h1' }]);
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
