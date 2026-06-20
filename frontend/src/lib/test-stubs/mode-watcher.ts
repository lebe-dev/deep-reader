// Test-only stub for `mode-watcher`, which only ships a `svelte` export
// condition and therefore can't be resolved under vitest's node environment.
// Aliased in vitest.config.ts so modules importing mode-watcher resolve in
// tests; individual tests still override these with `vi.mock` spies as needed.

export function setMode(_mode: string): void {}
export function setTheme(_theme: string): void {}
export const mode = { current: undefined as 'light' | 'dark' | undefined };
export const theme = { current: undefined as string | undefined };
