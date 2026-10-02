// Scaffold smoke test (SCAFFOLD-0b acceptance): the SPA shell renders under the
// production CSP with enforced Trusted Types (zero violations, including the
// service worker registration), in en and in the pseudo-locale en-XA.
import { expect, test } from '@playwright/test';

test('the shell renders with no CSP or Trusted Types violations', async ({ page }, info) => {
  const problems: string[] = [];
  page.on('console', (m) => {
    if (m.type() === 'error') problems.push(m.text());
  });
  page.on('pageerror', (e) => problems.push(e.message));
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      console.error(`CSP violation: ${e.violatedDirective} ${e.blockedURI}`);
    });
  });
  const res = await page.goto('/');
  expect(res?.headers()['content-security-policy']).toContain("require-trusted-types-for 'script'");
  expect(res?.headers()['tdm-reservation']).toBe('1');
  const heading = page.getByRole('heading', { level: 1 });
  await expect(heading).toBeVisible();
  if (info.project.name === 'en-XA') {
    await expect(heading).toHaveText(/^\[.*\]$/);
  }
  await page.goto('/no/such/page');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  expect(problems).toEqual([]);
});

test('unknown API routes get the uniform not-found', async ({ request, baseURL }) => {
  const res = await request.get('/api/definitely/not/here', { headers: { 'Sec-Fetch-Site': 'same-origin' } });
  expect(res.status()).toBe(404);
  expect(await res.text()).toBe('{"error":"not_found"}\n');
  const cross = await request.post('/api/auth/logout', { headers: { Origin: 'https://evil.example' } });
  expect(cross.status()).toBe(404);
  expect(baseURL).toBeTruthy();
});
