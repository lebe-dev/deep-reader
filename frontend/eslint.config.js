import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import tseslint from '@typescript-eslint/eslint-plugin';
import tsParser from '@typescript-eslint/parser';
import svelteParser from 'svelte-eslint-parser';
import globals from 'globals';

/** @type {import('eslint').Linter.Config[]} */
export default [
	{
		ignores: ['src/lib/components/ui/**', 'build/**', '.svelte-kit/**', 'node_modules/**']
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
				...globals.node
			}
		},
		plugins: {
			'@typescript-eslint': tseslint
		},
		rules: {
			...tseslint.configs.recommended.rules
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
				...globals.browser
			}
		},
		plugins: {
			svelte,
			'@typescript-eslint': tseslint
		},
		rules: {
			...svelte.configs.recommended.rules
		}
	}
];
