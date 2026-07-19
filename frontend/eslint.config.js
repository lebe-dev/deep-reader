import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import tseslint from '@typescript-eslint/eslint-plugin';
import tsParser from '@typescript-eslint/parser';
import svelteParser from 'svelte-eslint-parser';
import globals from 'globals';

// Svelte 5 runes are compiler-provided globals; declare them so `no-undef`
// does not flag `.svelte.ts`/`.svelte.js` modules (the .svelte parser injects
// them for component files, but plain rune modules are parsed as TS).
const runes = {
	$state: 'readonly',
	$derived: 'readonly',
	$effect: 'readonly',
	$props: 'readonly',
	$bindable: 'readonly',
	$inspect: 'readonly',
	$host: 'readonly'
};

// Compile-time constants injected by Vite `define` (vite.config.ts).
const buildConstants = {
	__APP_VERSION__: 'readonly'
};

// Shared no-unused-vars config: defer to the type-aware rule and let `_`-prefixed
// names mark deliberately-unused params/vars (e.g. `(_event) => …`, `{#each … as _, i}`).
const unusedVars = {
	'no-unused-vars': 'off',
	'@typescript-eslint/no-unused-vars': [
		'error',
		{ argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' }
	]
};

/** @type {import('eslint').Linter.Config[]} */
export default [
	{
		ignores: [
			'src/lib/components/ui/**',
			'build/**',
			'.svelte-kit/**',
			'node_modules/**',
			// Capacitor native projects: generated Xcode/Gradle sources plus a copy
			// of the built web bundle (ios|android/.../public). Never our source.
			'ios/**',
			'android/**'
		]
	},
	js.configs.recommended,
	{
		files: ['**/*.ts'],
		languageOptions: {
			parser: tsParser,
			parserOptions: {
				extraFileExtensions: ['.svelte']
			},
			globals: {
				...globals.browser,
				...globals.node,
				...runes,
				...buildConstants
			}
		},
		plugins: {
			'@typescript-eslint': tseslint
		},
		rules: {
			...tseslint.configs.recommended.rules,
			...unusedVars
		}
	},
	{
		// The service worker runs in a worker scope, not a window/document one.
		files: ['src/service-worker.ts'],
		languageOptions: {
			globals: {
				...globals.serviceworker
			}
		}
	},
	{
		files: ['**/*.svelte'],
		languageOptions: {
			parser: svelteParser,
			parserOptions: {
				parser: tsParser
			},
			globals: {
				...globals.browser,
				...runes,
				...buildConstants
			}
		},
		plugins: {
			svelte,
			'@typescript-eslint': tseslint
		},
		rules: {
			...svelte.configs.recommended.rules,
			...unusedVars
		}
	}
];
