import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist', 'node_modules'] },
  {
    // The React app.
    files: ['src/**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended, reactHooks.configs.flat.recommended],
    languageOptions: { globals: globals.browser },
    plugins: { 'react-refresh': reactRefresh },
    rules: {
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      // #147: localeCompare with no locale follows the reader's locale, so two
      // readers saw the same list in different orders. A collator made with no
      // locale is the same by another door.
      'no-restricted-syntax': [
        'error',
        {
          selector: "MemberExpression[property.name='localeCompare']",
          message:
            "localeCompare orders by the reader's locale, so two readers see different orders (#147). Pick one of the two helpers of src/model/order.ts: compareText for names, keys and timestamps, compareTitle for titles a person wrote.",
        },
        {
          selector: ":matches(NewExpression, CallExpression)[callee.property.name='Collator'][arguments.length=0]",
          message:
            "Intl.Collator with no locale orders by the reader's locale, so two readers see different orders (#147). Pick one of the two helpers of src/model/order.ts: compareText for names, keys and timestamps, compareTitle for titles a person wrote.",
        },
      ],
    },
  },
  {
    // Node code: the server, its tests, the screenshot scripts and the Vite config.
    files: ['*.{js,mjs,ts}', 'test/**/*.mjs', 'scripts/**/*.mjs'],
    extends: [js.configs.recommended],
    languageOptions: { globals: globals.node },
  },
  {
    files: ['vite.config.ts'],
    extends: [...tseslint.configs.recommended],
  },
)
