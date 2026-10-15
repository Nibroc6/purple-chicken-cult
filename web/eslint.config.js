// Flat ESLint config (SCAFFOLD-0b; plan §6.1, §6.5).
import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import i18next from 'eslint-plugin-i18next';
import globals from 'globals';

export default tseslint.config(
  {
    ignores: ['dist/**', 'node_modules/**', 'src/api/schema.gen.ts', 'src/i18n/locales/**', 'playwright-report/**', 'test-results/**'],
  },
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  {
    languageOptions: {
      parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname },
      globals: { ...globals.browser },
    },
    rules: {
      '@typescript-eslint/restrict-template-expressions': ['error', { allowNumber: true }],
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    plugins: { 'react-hooks': reactHooks, i18next },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // Every user-facing string comes from the catalogs (§15.4).
      'i18next/no-literal-string': [
        'error',
        {
          mode: 'jsx-only',
          'jsx-attributes': { include: ['alt', 'title', 'aria-label', 'placeholder'], exclude: ['.*'] },
        },
      ],
      // No HTML/script sinks (plan §6.1, §6.6).
      'no-restricted-syntax': [
        'error',
        {
          selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
          message: 'Never render HTML strings; Markdown renders to React elements.',
        },
        { selector: 'CallExpression[callee.name="eval"]', message: 'No eval.' },
      ],
      'no-restricted-properties': [
        'error',
        { property: 'innerHTML', message: 'No HTML sinks (Trusted Types: pcc-forbid).' },
        { property: 'outerHTML', message: 'No HTML sinks (Trusted Types: pcc-forbid).' },
        { property: 'insertAdjacentHTML', message: 'No HTML sinks (Trusted Types: pcc-forbid).' },
        { object: 'document', property: 'write', message: 'No HTML sinks (Trusted Types: pcc-forbid).' },
      ],
    },
  },
  {
    // Formats and integrity components are layout-only: they never fetch.
    files: ['src/formats/**', 'src/integrity/**'],
    rules: {
      'no-restricted-imports': [
        'error',
        { patterns: [{ group: ['**/api', '**/api/*'], message: 'Formats render view-models only (§15.1).' }] },
      ],
    },
  },
  {
    files: ['src/**/*.test.{ts,tsx}', 'src/test/**', 'e2e/**'],
    rules: { 'i18next/no-literal-string': 'off' },
  },
  {
    files: ['scripts/**/*.mjs', '*.config.{js,ts}', 'e2e/**/*.ts'],
    languageOptions: { globals: { ...globals.node } },
  },
  {
    files: ['scripts/**/*.mjs', 'eslint.config.js'],
    ...tseslint.configs.disableTypeChecked,
  },
);
