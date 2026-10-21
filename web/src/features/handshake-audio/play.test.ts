// play() on a fake AudioContext: it renders the sound into one buffer at the
// context's rate, resumes a suspended context, resolves when playback ends,
// and rejects bad bits. Owner: P1-W13.
import { describe, expect, it, vi } from 'vitest';
import { createHandshakeAudio } from './createHandshakeAudio';
import { decodePcm } from './decoder';

function fakeContext(rate: number) {
  const played: Float32Array[] = [];
  const ctx = {
    sampleRate: rate,
    state: 'suspended',
    destination: {},
    resume: vi.fn(() => {
      ctx.state = 'running';
      return Promise.resolve();
    }),
    createBuffer: (_ch: number, n: number) => {
      const data = new Float32Array(n);
      return {
        copyToChannel: (src: Float32Array) => {
          data.set(src);
        },
        data,
      };
    },
    createBufferSource: () => {
      const src = {
        buffer: undefined as { data: Float32Array } | undefined,
        onended: undefined as (() => void) | undefined,
        connect: vi.fn(),
        disconnect: vi.fn(),
        start: () => {
          if (src.buffer) played.push(src.buffer.data);
          setTimeout(() => src.onended?.(), 0);
        },
      };
      return src;
    },
  };
  return { ctx, played };
}

describe('play', () => {
  it('plays one decodable buffer and resolves when it ends', async () => {
    const { ctx, played } = fakeContext(44100);
    const audio = createHandshakeAudio(ctx as unknown as AudioContext);
    const bits = {
      ultra: Uint8Array.of(1, 2, 3, 4),
      jingle: Uint8Array.of(5, 6, 7),
    };
    const r = await audio.play(bits, { quiet: false });
    expect(typeof r.startedAt).toBe('number');
    expect(ctx.resume).toHaveBeenCalled();
    expect(played).toHaveLength(1);
    const pcm = new Float32Array(44100 * 3);
    pcm.set(played[0] ?? new Float32Array(0), 4410);
    const rep = decodePcm(pcm, 44100);
    expect(rep.ultraBits).toEqual(bits.ultra);
    expect(rep.jingleBits).toEqual(bits.jingle);
  });

  it('rejects bits of the wrong length', async () => {
    const { ctx } = fakeContext(48000);
    await expect(
      createHandshakeAudio(ctx as unknown as AudioContext).play(
        { ultra: new Uint8Array(2), jingle: new Uint8Array(3) },
        { quiet: false },
      ),
    ).rejects.toThrow('bad_bits');
  });
});
