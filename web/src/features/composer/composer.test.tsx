// Composer: the thread splitter and the compose page against a stubbed API.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { me, postJSON } from '../post/testdata';
import { codePoints, MAX_PARTS, SOFT_LIMIT } from '../post/vm';
import ComposePage from './ComposePage';
import { blocks, joinParts, sentences, splitThread } from './split';

interface Call {
  method: string;
  path: string;
  body: unknown;
}

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubAPI(handler: (c: Call) => Response | undefined, meOver: Record<string, unknown> = {}): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const json_ = req.headers.get('Content-Type')?.includes('json') ?? false;
      const text = json_ ? await req.clone().text() : '';
      const c = { method: req.method, path: new URL(req.url).pathname.replace(/^\/api/, ''), body: text ? (JSON.parse(text) as unknown) : undefined };
      calls.push(c);
      if (c.path === '/session') {
        return json(200, { authenticated: true, kind: 'user', csrf: 'x', me: { ...me, settings: { ...me.settings, ...meOver } } });
      }
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderAt(path: string) {
  const router = createMemoryRouter([{ path: '/compose', element: <ComposePage /> }], { initialEntries: [path] });
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

afterEach(() => {
  vi.unstubAllGlobals();
});

const sentence = (i: number) => `This is sentence number ${String(i)} of a rather long paragraph that goes on.`;

describe('splitThread', () => {
  it('keeps short text whole and splits at paragraphs first', () => {
    expect(splitThread('hello')).toEqual(['hello']);
    expect(splitThread('  \n ')).toEqual([]);
    const a = 'a'.repeat(300);
    const b = 'b'.repeat(300);
    expect(splitThread(`${a}\n\n${b}`)).toEqual([a, b]);
    expect(splitThread(`${'a'.repeat(100)}\n\n${'b'.repeat(100)}\n\n${'c'.repeat(400)}`)).toEqual([
      `${'a'.repeat(100)}\n\n${'b'.repeat(100)}`,
      'c'.repeat(400),
    ]);
  });

  it('splits a long paragraph at sentence boundaries', () => {
    const par = Array.from({ length: 20 }, (_, i) => sentence(i)).join(' ');
    const parts = splitThread(par);
    expect(parts.length).toBeGreaterThan(1);
    for (const p of parts) {
      expect(codePoints(p)).toBeLessThanOrEqual(SOFT_LIMIT);
      expect(p.endsWith('on.')).toBe(true);
    }
    expect(parts.join(' ')).toBe(par);
  });

  it('splits CJK text at its sentence marks', () => {
    const s = '这是一个很长的句子，用来测试中文的分句功能是否正常工作。';
    const par = s.repeat(40);
    const parts = splitThread(par);
    expect(parts.length).toBeGreaterThan(1);
    for (const p of parts) {
      expect(codePoints(p)).toBeLessThanOrEqual(SOFT_LIMIT);
      expect(p.endsWith('。')).toBe(true);
    }
    expect(parts.join('')).toBe(par);
  });

  it('never cuts an emoji in half', () => {
    const family = '👨‍👩‍👧‍👦';
    const text = family.repeat(300); // one "word" of 2,100 code points
    const parts = splitThread(text);
    expect(parts.length).toBeGreaterThan(1);
    for (const p of parts) {
      expect(codePoints(p)).toBeLessThanOrEqual(SOFT_LIMIT);
      expect(p.replaceAll(family, '')).toBe('');
    }
    expect(parts.join('')).toBe(text);
  });

  it('never splits a fenced code block', () => {
    const code = ['```go', ...Array.from({ length: 60 }, (_, i) => `x := ${String(i)} // a line of code here`), '', 'more()', '```'].join(
      '\n',
    );
    const text = `Intro paragraph.\n\n${code}\n\nOutro paragraph.`;
    expect(blocks(text).map((b) => b.code)).toEqual([false, true, false]);
    const parts = splitThread(text);
    expect(parts.filter((p) => p.includes('```'))).toEqual([code]);
    expect(codePoints(code)).toBeGreaterThan(SOFT_LIMIT);
  });

  it('caps a thread at 25 parts by growing the parts', () => {
    const text = Array.from({ length: 400 }, (_, i) => sentence(i)).join('\n\n');
    expect(codePoints(text)).toBeGreaterThan(MAX_PARTS * SOFT_LIMIT);
    const parts = splitThread(text);
    expect(parts.length).toBeLessThanOrEqual(MAX_PARTS);
    expect(joinParts(parts)).toBe(text);
  });

  it('segments sentences', () => {
    expect(sentences('One. Two! Three?')).toEqual(['One. ', 'Two! ', 'Three?']);
  });
});

describe('compose page', () => {
  it('publishes with the language guess, the counter and the chosen audience', async () => {
    const calls = stubAPI((c) => (c.method === 'POST' && c.path === '/posts' ? json(201, postJSON()) : undefined));
    renderAt('/compose');
    const box = await screen.findByTestId('composer-body');
    expect(screen.getByTestId('post-lang-auto').dataset.lang).toBe('de');
    expect((screen.getByTestId<HTMLSelectElement>('post-policy')).value).toBe('anyone');
    fireEvent.change(box, { target: { value: '😀'.repeat(279) } });
    expect(screen.queryByTestId('composer-counter')).toBeNull();
    fireEvent.change(box, { target: { value: '😀'.repeat(280) } });
    expect(screen.getByTestId('composer-counter').dataset.state).toBe('normal');
    fireEvent.change(box, { target: { value: '😀'.repeat(501) } });
    expect(screen.getByTestId('composer-counter').dataset.state).toBe('amber');
    fireEvent.change(box, { target: { value: '😀'.repeat(20001) } });
    expect(screen.getByTestId('composer-counter').dataset.state).toBe('over');
    expect((screen.getByTestId<HTMLButtonElement>('composer-publish')).disabled).toBe(true);
    fireEvent.change(box, { target: { value: 'hello' } });
    fireEvent.change(screen.getByTestId('post-audience'), { target: { value: 'd2' } });
    fireEvent.click(screen.getByTestId('composer-publish'));
    await screen.findByTestId('composer-published');
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ body: 'hello', lang: 'de', audience: 'd2', ref_policy: 'anyone' });
    expect(screen.getByTestId('composer-manage').getAttribute('href')).toBe('/p/p1/edit');
    fireEvent.click(screen.getByTestId('composer-again'));
    expect((await screen.findByTestId<HTMLTextAreaElement>('composer-body')).value).toBe('');
  });

  it('detects the language from the text, and a choice overrides it', async () => {
    const calls = stubAPI((c) => (c.method === 'POST' && c.path === '/posts' ? json(201, postJSON()) : undefined));
    renderAt('/compose');
    const box = await screen.findByTestId('composer-body');
    fireEvent.change(box, { target: { value: 'Les poules sont dans la cour et je suis très content' } });
    expect(screen.getByTestId('post-lang-auto').dataset.lang).toBe('fr');
    expect(screen.getByTestId('post-lang-auto').textContent).toContain(tr('post:lang.detected', { name: 'French' }));
    fireEvent.click(screen.getByTestId('post-lang-change'));
    const select = screen.getByTestId<HTMLSelectElement>('post-lang');
    expect(select.value).toBe('fr');
    fireEvent.change(select, { target: { value: 'it' } });
    fireEvent.change(box, { target: { value: 'the chickens are out in the yard again' } });
    expect(select.value).toBe('it');
    fireEvent.click(screen.getByTestId('composer-publish'));
    await screen.findByTestId('composer-published');
    expect((calls.find((c) => c.method === 'POST')?.body as { lang: string }).lang).toBe('it');
  });

  it('defaults to slippery when the settings say so', async () => {
    stubAPI(() => undefined, { slippery_default: true, languages: [] });
    renderAt('/compose');
    await screen.findByTestId('composer-body');
    expect((screen.getByTestId<HTMLSelectElement>('post-policy')).value).toBe('nobody');
    expect(screen.getByTestId('post-lang-auto').dataset.lang).toBe('en');
  });

  it('splits into an editable thread and publishes it in one request', async () => {
    const calls = stubAPI((c) =>
      c.method === 'POST' && c.path === '/posts/thread'
        ? json(201, { items: [postJSON({ thread: { index: 1, total: 2 } }), postJSON({ ref: 'p2', thread: { index: 2, total: 2 } })] })
        : undefined,
    );
    renderAt('/compose');
    const text = `${'a'.repeat(400)}\n\n${'b'.repeat(400)}`;
    fireEvent.change(await screen.findByTestId('composer-body'), { target: { value: text } });
    fireEvent.click(screen.getByTestId('composer-split'));
    const parts = screen.getAllByTestId('composer-part');
    expect(parts).toHaveLength(2);
    fireEvent.change(parts[1] as HTMLElement, { target: { value: 'b, shorter' } });
    expect(screen.getByTestId('composer-publish').textContent).toBe(tr('composer:publish.thread', { count: 2 }));
    fireEvent.click(screen.getByTestId('composer-publish'));
    await screen.findByTestId('composer-published');
    expect(calls.find((c) => c.path === '/posts/thread')?.body).toEqual({
      parts: [
        { body: 'a'.repeat(400), lang: 'de' },
        { body: 'b, shorter', lang: 'de' },
      ],
      audience: 'connected',
      ref_policy: 'anyone',
    });
    expect(screen.getByTestId('composer-manage').getAttribute('href')).toBe('/p/p2/edit');
  });

  it('merges a split back into one post', async () => {
    stubAPI(() => undefined);
    renderAt('/compose');
    fireEvent.change(await screen.findByTestId('composer-body'), { target: { value: `${'a'.repeat(400)}\n\n${'b'.repeat(400)}` } });
    fireEvent.click(screen.getByTestId('composer-split'));
    fireEvent.click(screen.getByTestId('composer-unsplit'));
    expect((screen.getByTestId<HTMLTextAreaElement>('composer-body')).value).toBe(`${'a'.repeat(400)}\n\n${'b'.repeat(400)}`);
  });

  it('warns when replying to a post with a limited audience and sends the parent', async () => {
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/par') {
        return json(200, postJSON({ ref: 'par', mine: false, audience: 'd1', author: { ...postJSON().author, handle: 'bob' } }));
      }
      if (c.method === 'POST' && c.path === '/posts') return json(201, postJSON());
      return undefined;
    });
    renderAt('/compose?parent=par&kind=ref');
    expect(await screen.findByTestId('composer-audience-warning')).toBeTruthy();
    expect(screen.getByText(tr('composer:parent.ref', { handle: 'bob' }))).toBeTruthy();
    fireEvent.change(screen.getByTestId('composer-body'), { target: { value: 'a reply' } });
    fireEvent.click(screen.getByTestId('composer-publish'));
    await screen.findByTestId('composer-published');
    expect((calls.find((c) => c.method === 'POST')?.body as { parents: unknown }).parents).toEqual([{ ref: 'par', kind: 'ref' }]);
  });

  it('refuses replies the parent does not accept and unknown parents', async () => {
    stubAPI((c) =>
      c.path === '/posts/slip' ? json(200, postJSON({ ref: 'slip', mine: false, can_reference: false, ref_policy: 'nobody' })) : undefined,
    );
    renderAt('/compose?parent=slip&kind=ref');
    expect(await screen.findByTestId('composer-parent-closed')).toBeTruthy();
    fireEvent.change(screen.getByTestId('composer-body'), { target: { value: 'x' } });
    expect((screen.getByTestId<HTMLButtonElement>('composer-publish')).disabled).toBe(true);
  });

  it('says when the parent cannot be found', async () => {
    stubAPI(() => undefined);
    renderAt('/compose?parent=gone&kind=cont');
    expect(await screen.findByTestId('composer-parent-missing')).toBeTruthy();
    await waitFor(() => {
      expect((screen.getByTestId<HTMLButtonElement>('composer-publish')).disabled).toBe(true);
    });
  });

  it('offers the friend+ audience', async () => {
    const calls = stubAPI((c) => (c.method === 'POST' && c.path === '/posts' ? json(201, postJSON({ audience: 'friend_plus' })) : undefined));
    renderAt('/compose');
    fireEvent.change(await screen.findByTestId('composer-body'), { target: { value: 'close friends' } });
    fireEvent.change(screen.getByTestId('post-audience'), { target: { value: 'friend_plus' } });
    fireEvent.click(screen.getByTestId('composer-publish'));
    await screen.findByTestId('composer-published');
    expect((calls.find((c) => c.method === 'POST')?.body as { audience: string }).audience).toBe('friend_plus');
  });

  it('attaches uploaded pictures in order, removes one, and sends their refs', async () => {
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:x', revokeObjectURL: () => undefined }));
    let n = 0;
    const calls = stubAPI((c) => {
      if (c.method === 'POST' && c.path === '/media') {
        n++;
        return json(201, { ref: `m${String(n)}`, w: 40, h: 30 });
      }
      if (c.path.endsWith('/url')) return json(200, { url: `/m/k${c.path}?e=1`, expires_at: '2099-01-01T00:00:00Z' });
      if (c.method === 'POST' && c.path === '/posts') {
        return json(201, postJSON({ media: [{ ref: 'm1', w: 40, h: 30 }, { ref: 'm3', w: 40, h: 30 }] }));
      }
      return undefined;
    });
    renderAt('/compose');
    fireEvent.change(await screen.findByTestId('composer-body'), { target: { value: 'pictures' } });
    const png = (name: string) => new File([new Uint8Array(4)], name, { type: 'image/png' });
    fireEvent.change(screen.getByTestId('media-file'), { target: { files: [png('a.png'), png('b.png'), png('c.png')] } });
    fireEvent.click(screen.getByTestId('media-upload'));
    await waitFor(() => {
      expect(screen.getAllByTestId('composer-media-item')).toHaveLength(3);
    });
    expect(screen.getByTestId('composer-media-count').textContent).toBe(tr('composer:media.attached', { count: 3 }));
    fireEvent.click(screen.getAllByTestId('composer-media-remove')[1] as HTMLElement);
    expect(screen.getAllByTestId('composer-media-item')).toHaveLength(2);
    fireEvent.click(screen.getByTestId('composer-publish'));
    await screen.findByTestId('composer-published');
    expect((calls.find((c) => c.method === 'POST' && c.path === '/posts')?.body as { media: string[] }).media).toEqual(['m1', 'm3']);
    expect(screen.getByTestId('post-preview-media').querySelectorAll('[data-testid=media-image]')).toHaveLength(2);
  });

  it('hides the picker once four pictures are attached', async () => {
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:x', revokeObjectURL: () => undefined }));
    let n = 0;
    stubAPI((c) => {
      if (c.method === 'POST' && c.path === '/media') return json(201, { ref: `m${String(++n)}`, w: 4, h: 3 });
      if (c.path.endsWith('/url')) return json(200, { url: '/m/k?e=1', expires_at: '2099-01-01T00:00:00Z' });
      return undefined;
    });
    renderAt('/compose');
    await screen.findByTestId('composer-body');
    const files = Array.from({ length: 5 }, (_, i) => new File([new Uint8Array(4)], `${String(i)}.png`, { type: 'image/png' }));
    fireEvent.change(screen.getByTestId('media-file'), { target: { files } });
    expect(screen.getAllByTestId('media-item')).toHaveLength(4);
    fireEvent.click(screen.getByTestId('media-upload'));
    await waitFor(() => {
      expect(screen.getAllByTestId('composer-media-item')).toHaveLength(4);
    });
    expect(screen.queryByTestId('media-picker')).toBeNull();
  });

  it('shows Problems from the post catalog', async () => {
    stubAPI((c) => (c.method === 'POST' ? json(409, { error: 'ref_cap', field: 'parents' }) : undefined));
    renderAt('/compose');
    fireEvent.change(await screen.findByTestId('composer-body'), { target: { value: 'x' } });
    fireEvent.click(screen.getByTestId('composer-publish'));
    expect(await screen.findByText(tr('post:errors.ref_cap'))).toBeTruthy();
  });

  it('localizes too_many_media', async () => {
    stubAPI((c) => (c.method === 'POST' ? json(422, { error: 'too_many_media', field: 'media' }) : undefined));
    renderAt('/compose');
    fireEvent.change(await screen.findByTestId('composer-body'), { target: { value: 'x' } });
    fireEvent.click(screen.getByTestId('composer-publish'));
    expect(await screen.findByText(tr('post:errors.too_many_media'))).toBeTruthy();
  });
});
