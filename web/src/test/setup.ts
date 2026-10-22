// Vitest setup: English catalogs, all namespaces preloaded. After every test
// the rendered page is checked for an ICU template that was never formatted
// (a message shown without its arguments, like "{seconds, plural, …}").
import { cleanup } from '@testing-library/react';
import { afterEach, beforeAll } from 'vitest';
import { allNamespaces, i18n, setupI18n } from '../i18n/setup';
import { rawICU } from './icu';

beforeAll(async () => {
  await setupI18n('en');
  await i18n.loadNamespaces(allNamespaces);
});

afterEach(() => {
  const attrs = Array.from(document.body.querySelectorAll('*'), (el) => Array.from(el.attributes, (a) => a.value).join(' '));
  const raw = rawICU(`${document.body.textContent} ${attrs.join(' ')}`);
  cleanup();
  if (raw) throw new Error(`unformatted ICU message on the page: ${raw}`);
});
