// Vitest setup: English catalogs, all namespaces preloaded.
import { cleanup } from '@testing-library/react';
import { afterEach, beforeAll } from 'vitest';
import { allNamespaces, i18n, setupI18n } from '../i18n/setup';

beforeAll(async () => {
  await setupI18n('en');
  await i18n.loadNamespaces(allNamespaces);
});

afterEach(() => {
  cleanup();
});
