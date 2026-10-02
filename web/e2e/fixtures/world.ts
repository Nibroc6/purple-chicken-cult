// Test-hook client (/api/_test/*, PCC_ENV=test + PCC_TEST_HOOKS=1 only). WP specs
// use only these hooks plus their own fixture testdata/fixtures/<wp-id>.json.
import type { APIRequestContext } from '@playwright/test';

export class World {
  constructor(
    private readonly request: APIRequestContext,
    private readonly origin: string,
  ) {}

  private post(path: string, data: unknown) {
    return this.request.post(`/api/_test/${path}`, {
      data,
      headers: { Origin: this.origin, 'Sec-Fetch-Site': 'same-origin' },
    });
  }

  async reset(): Promise<void> {
    await this.post('reset', {});
  }

  async seed(fixture: string): Promise<void> {
    const res = await this.post('seed', { fixture });
    if (!res.ok()) throw new Error(`seed ${fixture}: ${String(res.status())}`);
  }

  async advance(duration: string): Promise<void> {
    await this.post('clock', { advance: duration });
  }

  // login sets the session cookie on the request context's cookie jar and returns the CSRF token.
  async login(handle: string): Promise<string> {
    const res = await this.post('login', { handle });
    if (!res.ok()) throw new Error(`login ${handle}: ${String(res.status())}`);
    const body = (await res.json()) as { csrf: string };
    return body.csrf;
  }

  async genesisToken(handle: string): Promise<string> {
    const res = await this.post('genesis', { handle });
    const body = (await res.json()) as { token: string };
    return body.token;
  }
}
