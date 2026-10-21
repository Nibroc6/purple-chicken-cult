// The sound check page and play() on a fake AudioContext. Owner: P1-W13.
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import '../../i18n/setup';
import type { DecodeReport, HandshakeAudio, PlayBits } from './index';

const fake = vi.hoisted(() => ({
  play: vi.fn<(bits: PlayBits, opts: { quiet: boolean }) => Promise<{ startedAt: number }>>(),
  listen: vi.fn<() => Promise<DecodeReport>>(),
}));

vi.mock('./createHandshakeAudio', () => ({
  createHandshakeAudio: (): HandshakeAudio => ({
    play: fake.play,
    listen: fake.listen,
  }),
}));

const { default: SoundCheckPage } = await import('./SoundCheckPage');

beforeEach(() => {
  vi.stubGlobal(
    'AudioContext',
    class {
      sampleRate = 48000;
    },
  );
  fake.play.mockReset();
  fake.listen.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('SoundCheckPage', () => {
  it('plays a sound and shows its code', async () => {
    fake.play.mockResolvedValue({ startedAt: 0 });
    render(<SoundCheckPage />);
    fireEvent.click(screen.getByTestId('sound-check-play'));
    const played = await screen.findByTestId('sound-check-played');
    const bits = fake.play.mock.calls[0]?.[0];
    const hex = (b: Uint8Array | undefined) => Array.from(b ?? [], (v) => v.toString(16).padStart(2, '0')).join('');
    expect(played.dataset.code).toBe(`${hex(bits?.jingle)} · ${hex(bits?.ultra)}`);
    expect(fake.play.mock.calls[0]?.[1]).toEqual({ quiet: false });
  });

  it('plays only the ultrasonic layer in quiet mode', async () => {
    fake.play.mockResolvedValue({ startedAt: 0 });
    render(<SoundCheckPage />);
    fireEvent.click(screen.getByTestId('sound-check-quiet'));
    fireEvent.click(screen.getByTestId('sound-check-play'));
    await screen.findByTestId('sound-check-played');
    expect(fake.play.mock.calls[0]?.[1]).toEqual({ quiet: true });
  });

  it('shows what it heard', async () => {
    fake.listen.mockResolvedValue({
      jingleBits: Uint8Array.of(1, 2, 3),
      motifLevelDb: -20,
    });
    render(<SoundCheckPage />);
    fireEvent.click(screen.getByTestId('sound-check-listen'));
    expect(screen.getByTestId('sound-check-listening')).toBeTruthy();
    expect((await screen.findByTestId('sound-check-jingle')).dataset.code).toBe('010203');
    expect(screen.getByTestId('sound-check-no-ultra')).toBeTruthy();
    expect(screen.queryByTestId('sound-check-volume')).toBeNull();
  });

  it('asks for more volume when the jingle was faint, and says when it heard nothing', async () => {
    fake.listen.mockResolvedValueOnce({
      jingleBits: Uint8Array.of(1, 2, 3),
      ultraBits: Uint8Array.of(9, 9, 9, 9),
      motifLevelDb: -60,
    });
    render(<SoundCheckPage />);
    fireEvent.click(screen.getByTestId('sound-check-listen'));
    expect(await screen.findByTestId('sound-check-volume')).toBeTruthy();
    expect(screen.getByTestId('sound-check-ultra').dataset.code).toBe('09090909');
    fake.listen.mockResolvedValueOnce({ motifLevelDb: -120 });
    fireEvent.click(screen.getByTestId('sound-check-listen'));
    expect(await screen.findByTestId('sound-check-nothing')).toBeTruthy();
  });

  it('reports a microphone it cannot use', async () => {
    fake.listen.mockRejectedValue(new DOMException('denied', 'NotAllowedError'));
    render(<SoundCheckPage />);
    fireEvent.click(screen.getByTestId('sound-check-listen'));
    expect(await screen.findByTestId('sound-check-error')).toBeTruthy();
  });
});
