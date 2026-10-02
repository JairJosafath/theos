// Lint config for the extension's TypeScript: typescript-eslint's strictest
// type-checked presets, plus the rules below. `make lint` runs ESLint with
// --max-warnings 0, so any finding fails `make check`. Every rule turned off
// or changed here says why. Changing this file is the Standard's job, not a
// task's.
import js from '@eslint/js';
import { defineConfig, globalIgnores } from 'eslint/config';
import tseslint from 'typescript-eslint';

export default defineConfig(
  globalIgnores(['out/', 'dist/', '.vscode-test/']),
  js.configs.recommended,
  tseslint.configs.strictTypeChecked,
  tseslint.configs.stylisticTypeChecked,
  {
    languageOptions: {
      parserOptions: {
        // Type information from tsconfig.json, for every file it includes.
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    linterOptions: {
      // A disable comment that silences nothing is dead code.
      reportUnusedDisableDirectives: 'error',
    },
    rules: {
      // A switch over a union or enum names every member, so a new member
      // fails the lint wherever it must be handled. A default branch does
      // not count.
      '@typescript-eslint/switch-exhaustiveness-check': [
        'error',
        { considerDefaultExhaustiveForUnions: false, requireDefaultForNonUnion: true },
      ],
      // Exported functions state their parameter and return types: they are
      // the module's contract.
      '@typescript-eslint/explicit-module-boundary-types': 'error',
      // Members never reassigned after construction are readonly.
      '@typescript-eslint/prefer-readonly': 'error',
      // Array#sort without a compare function sorts numbers as strings.
      '@typescript-eslint/require-array-sort-compare': ['error', { ignoreStringArrays: true }],
      // Users see an OutputChannel, never the extension host's console.
      'no-console': 'error',
      eqeqeq: ['error', 'always'],
      curly: ['error', 'all'],
    },
  },
  {
    // Build and config scripts are plain JavaScript, outside tsconfig.json.
    files: ['**/*.mjs'],
    extends: [tseslint.configs.disableTypeChecked],
  },
);
