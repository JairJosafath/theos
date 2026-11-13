import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

const HELPERS =
  'Pick one of the two helpers of src/model/order.ts: compareText for names, keys and timestamps, compareTitle for titles a person wrote.'

const LOCALE_COMPARE = {
  // Both spellings: .localeCompare and ['localeCompare'].
  selector:
    ":matches(MemberExpression[property.name='localeCompare'], MemberExpression[property.value='localeCompare'])",
  message: `localeCompare orders by the reader's locale, so two readers see different orders (#147). ${HELPERS}`,
}

// Any Intl.Collator, whatever its arguments, Intl['Collator'], a Collator
// taken out of Intl, and the type: no counting of arguments (#175).
const COLLATOR = [
  {
    selector: "Identifier[name='Collator']",
    message: `A Collator made outside src/model/order.ts follows the reader's locale or its own, so two readers see different orders (#175). ${HELPERS}`,
  },
  {
    selector: "Literal[value='Collator']",
    message: `A Collator made outside src/model/order.ts follows the reader's locale or its own, so two readers see different orders (#175). ${HELPERS}`,
  },
]

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
      // locale, or with the reader's, is the same by another door (#175): the
      // fixed collator is made once, in src/model/order.ts, so no other file
      // has a reason to make one, and the next block lets that file through.
      'no-restricted-syntax': ['error', LOCALE_COMPARE, ...COLLATOR],
    },
  },
  {
    // The one module that makes the collator, with a fixed locale, and its test.
    files: ['src/model/order.ts', 'src/model/order.test.ts'],
    rules: { 'no-restricted-syntax': ['error', LOCALE_COMPARE] },
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
