// Media feature: upload, the fail-closed picker, signed URLs and their refresh.
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { asRef, type MediaVM } from '../../vm/types';
import { MediaImage, MediaPicker } from './index';
import { setMediaDisabled } from './state';
import { clearURLs, signedURL } from './urls';

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

interface Seen {
  uploads: { alt: string; size: number }[];
  urls: string[];
}

function stub(upload: () => Response, url: (n: number) => Response = (n) => json(200, { url: `/m/k${String(n)}?e=1`, expires_at: '2099-01-01T00:00:00Z' })): Seen {
  const seen: Seen = { uploads: [], urls: [] };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      const path = u.pathname.replace(/^\/api/, '');
      if (path === '/media' && req.method === 'POST') {
        const form = await req.formData();
        const file = form.get('file') as File;
        seen.uploads.push({ alt: form.get('alt') as string, size: file.size });
        return upload();
      }
      if (path.endsWith('/url')) {
        seen.urls.push(`${path}?${u.searchParams.toString()}`);
        return url(seen.urls.length);
      }
      return json(404, { error: 'not_found' });
    }),
  );
  return seen;
}

const view = { ref: 'r1', w: 40, h: 30, alt: 'a pattern' };
const media: MediaVM = { ref: asRef('r1'), w: 40, h: 30, alt: 'a pattern' };

beforeEach(() => {
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:x', revokeObjectURL: () => undefined }));
});

afterEach(() => {
  act(() => {
    setMediaDisabled(false);
  });
  clearURLs();
  vi.unstubAllGlobals();
});

function pick(files: File[]) {
  fireEvent.change(screen.getByTestId('media-file'), { target: { files } });
}

const png = (name = 'p.png', n = 4) => new File([new Uint8Array(n)], name, { type: 'image/png' });

describe('MediaPicker', () => {
  it('uploads each chosen file with its alt text and hands back the media', async () => {
    const seen = stub(() => json(201, view));
    const onPicked = vi.fn();
    render(<MediaPicker max={4} onPicked={onPicked} />);
    pick([png('a.png', 3), png('b.png', 5)]);
    const alts = screen.getAllByTestId('media-alt');
    const first = alts[0];
    if (!first) throw new Error('no alt field');
    fireEvent.change(first, { target: { value: '  first  ' } });
    fireEvent.click(screen.getByTestId('media-upload'));
    await waitFor(() => {
      expect(onPicked).toHaveBeenCalledTimes(1);
    });
    expect(seen.uploads).toEqual([
      { alt: 'first', size: 3 },
      { alt: '', size: 5 },
    ]);
    expect(onPicked).toHaveBeenCalledWith([media, media]);
    expect(screen.queryAllByTestId('media-item')).toHaveLength(0);
  });

  it('keeps a refused picture with its error and passes on the rest', async () => {
    let n = 0;
    stub(() => (n++ === 0 ? json(422, { error: 'media_rejected', field: 'file' }) : json(201, view)));
    const onPicked = vi.fn();
    render(<MediaPicker max={4} onPicked={onPicked} />);
    pick([png('bad.png'), png('ok.png')]);
    fireEvent.click(screen.getByTestId('media-upload'));
    await waitFor(() => {
      expect(onPicked).toHaveBeenCalledWith([media]);
    });
    expect(screen.getAllByTestId('media-item')).toHaveLength(1);
    expect(screen.getByTestId('media-error').dataset.code).toBe('media_rejected');
  });

  it('accepts at most max files', () => {
    stub(() => json(201, view));
    render(<MediaPicker max={1} onPicked={vi.fn()} />);
    pick([png('a.png'), png('b.png')]);
    expect(screen.getAllByTestId('media-item')).toHaveLength(1);
    expect(screen.getByTestId<HTMLInputElement>('media-file').disabled).toBe(true);
  });

  it('hides itself once the server says media is turned off', async () => {
    stub(() => json(409, { error: 'feature_disabled', feature: 'media' }));
    const onPicked = vi.fn();
    render(<MediaPicker max={4} onPicked={onPicked} />);
    pick([png()]);
    fireEvent.click(screen.getByTestId('media-upload'));
    await waitFor(() => {
      expect(screen.queryByTestId('media-picker')).toBeNull();
    });
    expect(onPicked).not.toHaveBeenCalled();
  });

  it('refuses a file over 15 MB without sending it', async () => {
    const seen = stub(() => json(201, view));
    render(<MediaPicker max={4} onPicked={vi.fn()} />);
    const big = new File([new Uint8Array(1)], 'big.png', { type: 'image/png' });
    Object.defineProperty(big, 'size', { value: 16 * 1024 * 1024 });
    pick([big]);
    fireEvent.click(screen.getByTestId('media-upload'));
    await waitFor(() => {
      expect(screen.getByTestId('media-error').dataset.code).toBe('media_too_large');
    });
    expect(seen.uploads).toHaveLength(0);
  });
});

describe('MediaImage', () => {
  it('loads a signed URL and asks for a fresh one when the image fails (403 after expiry)', async () => {
    const seen = stub(() => json(201, view));
    render(<MediaImage media={media} variant="display" />);
    const img = await screen.findByTestId('media-image');
    await waitFor(() => {
      expect(img.getAttribute('src')).toBe('/m/k1?e=1');
    });
    expect((img).getAttribute('alt')).toBe('a pattern');
    expect((img).getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(seen.urls).toEqual(['/media/r1/url?variant=display']);
    fireEvent.error(img);
    await waitFor(() => {
      expect(screen.getByTestId('media-image').getAttribute('src')).toBe('/m/k2?e=1');
    });
    // A second failure in a row shows the placeholder.
    fireEvent.error(screen.getByTestId('media-image'));
    expect((await screen.findByTestId('media-missing')).getAttribute('aria-label')).toBe('a pattern');
  });

  it('shows the placeholder when the URL is refused', async () => {
    stub(
      () => json(201, view),
      () => json(404, { error: 'not_found' }),
    );
    render(<MediaImage media={media} variant="thumb" />);
    expect(await screen.findByTestId('media-missing')).toBeTruthy();
  });
});

describe('signed URL cache', () => {
  it('reuses a URL until shortly before it expires', async () => {
    let now = Date.parse('2026-01-01T00:00:00Z');
    const seen = stub(
      () => json(201, view),
      (n) => json(200, { url: `/m/k${String(n)}`, expires_at: '2026-01-01T01:00:00Z' }),
    );
    const clock = () => now;
    expect((await signedURL('r9', 'thumb', clock)).url).toBe('/m/k1');
    expect((await signedURL('r9', 'thumb', clock)).url).toBe('/m/k1');
    expect((await signedURL('r9', 'original', clock)).url).toBe('/m/k2');
    now += 59.5 * 60_000;
    expect((await signedURL('r9', 'thumb', clock)).url).toBe('/m/k3');
    expect(seen.urls).toHaveLength(3);
  });
});
