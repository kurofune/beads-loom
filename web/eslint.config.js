import tseslint from 'typescript-eslint'

/**
 * The integration boundary, and nothing else. Core code reaches an integration
 * only through the composition file `src/integrations/index.ts` (imported as
 * `@/integrations`); a deep import into `src/integrations/` from outside it is
 * the structural leak this gate exists to reject.
 */
export default [
  {
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/integrations/**'],
    languageOptions: {
      parser: tseslint.parser,
      parserOptions: { ecmaFeatures: { jsx: true } },
    },
    linterOptions: { noInlineConfig: true },
    rules: {
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['@/integrations/*', '**/integrations/**'],
              message:
                'Core code reaches integrations only through the composition file: import from "@/integrations".',
            },
          ],
        },
      ],
    },
  },
]
