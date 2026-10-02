import { describe, expect, it } from 'vitest';
import { ApiError, errorKey } from '../api/errors';
import { i18n, pickLocale } from '../i18n/setup';
import { formatRelative } from './time';
import { allowedScriptURL } from './trustedTypes';

describe('trusted types script-url policy', () => {
  const base = 'https://pcc.example/feed';
  it('allows only same-origin /sw.js and /assets/', () => {
    expect(allowedScriptURL('/sw.js', base, false)).toBe(true);
    expect(allowedScriptURL('/assets/scan-abc.js', base, false)).toBe(true);
    expect(allowedScriptURL('/src/worker.ts', base, false)).toBe(false);
    expect(allowedScriptURL('https://evil.example/assets/x.js', base, false)).toBe(false);
    expect(allowedScriptURL('data:text/javascript,1', base, false)).toBe(false);
  });
  it('allows dev module workers only in dev', () => {
    expect(allowedScriptURL('/src/worker.ts', base, true)).toBe(true);
  });
});

describe('i18n', () => {
  it('formats ICU plurals and selects', () => {
    expect(i18n.t('errors:rate_limited', { seconds: 1 })).toContain('1 second');
    expect(i18n.t('errors:rate_limited', { seconds: 5 })).toContain('5 seconds');
    expect(i18n.t('integrity:tier.start', { tier: 1 })).toBe('Friends');
    expect(i18n.t('integrity:tier.start', { tier: 4 })).toBe('4 steps away');
  });
  it('picks a supported locale', () => {
    expect(pickLocale(['de-DE', 'en-GB'])).toBe('en');
    expect(pickLocale(['en-XA'])).toBe('en-XA');
    expect(pickLocale([])).toBe('en');
  });
});

describe('api errors', () => {
  it('maps codes, never prose', () => {
    const e = new ApiError(404, { error: 'not_found' });
    expect(e.isNotFound).toBe(true);
    expect(errorKey(e)).toBe('errors:not_found');
    expect(new ApiError(500, { error: 'Some Prose Here' }).code).toBe('unknown');
    expect(new ApiError(401, { error: 'step_up_required' }).isStepUp).toBe(true);
  });
});

describe('time', () => {
  it('formats relative times with Intl', () => {
    const now = new Date('2026-01-01T12:00:00Z');
    expect(formatRelative(new Date('2026-01-01T03:00:00Z'), 'en', now)).toMatch(/9/);
  });
});
