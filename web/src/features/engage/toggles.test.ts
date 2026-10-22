import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeToggler, SETTLE_MS } from './toggles';

function setup(answer: (on: boolean, n: number) => Promise<string> = (on) => Promise.resolve(on ? 'on' : 'off')) {
  const sent: boolean[] = [];
  const landed: boolean[] = [];
  const failed: [boolean, unknown][] = [];
  const tg = makeToggler<string>({
    send: (_ref, on) => {
      sent.push(on);
      return answer(on, sent.length);
    },
    landed: (_ref, on) => landed.push(on),
    failed: (_ref, confirmed, err) => failed.push([confirmed, err]),
  });
  return { tg, sent, landed, failed };
}

describe('toggle coalescing', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('sends nothing for a quick save and unsave', async () => {
    const { tg, sent } = setup();
    tg.set('p1', true, false);
    tg.set('p1', false, true);
    await vi.advanceTimersByTimeAsync(SETTLE_MS * 2);
    expect(sent).toEqual([]);
  });

  it('sends only the final state of a burst', async () => {
    const { tg, sent, landed } = setup();
    for (let i = 0; i < 7; i++) tg.set('p1', i % 2 === 0, i % 2 !== 0);
    await vi.advanceTimersByTimeAsync(SETTLE_MS * 2);
    expect(sent).toEqual([true]);
    expect(landed).toEqual([true]);
  });

  it('queues clicks made while a request is in flight', async () => {
    let release: (() => void) | undefined;
    const { tg, sent, landed } = setup(
      (on, n) =>
        n === 1
          ? new Promise((r) => {
              release = () => {
                r('on');
              };
            })
          : Promise.resolve(on ? 'on' : 'off'),
    );
    tg.set('p1', true, false);
    await vi.advanceTimersByTimeAsync(SETTLE_MS);
    expect(sent).toEqual([true]);
    tg.set('p1', false, true);
    tg.set('p1', true, false);
    tg.set('p1', false, true);
    release?.();
    await vi.advanceTimersByTimeAsync(SETTLE_MS);
    expect(sent).toEqual([true, false]);
    expect(landed).toEqual([false]); // the in-between "on" is never shown as final
  });

  it('waits out a short rate limit once instead of failing', async () => {
    const { tg, sent, landed, failed } = setup((on, n) =>
      n === 1 ? Promise.reject(new ApiError(429, { error: 'rate_limited', retry_after_s: 2 })) : Promise.resolve(on ? 'on' : 'off'),
    );
    tg.set('p1', true, false);
    await vi.advanceTimersByTimeAsync(SETTLE_MS + 2200);
    expect(sent).toEqual([true, true]);
    expect(landed).toEqual([true]);
    expect(failed).toEqual([]);
  });

  it('reports a long rate limit with the state the server is still in', async () => {
    const err = new ApiError(429, { error: 'rate_limited', retry_after_s: 30 });
    const { tg, failed } = setup(() => Promise.reject(err));
    tg.set('p1', true, false);
    await vi.advanceTimersByTimeAsync(SETTLE_MS);
    expect(failed).toEqual([[false, err]]);
  });
});
