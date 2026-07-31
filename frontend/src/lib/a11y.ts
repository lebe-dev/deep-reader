// Accessibility helpers shared across the app.
//
// The CSS side of "reduce motion" lives in app.css; this is the JavaScript side
// — animations the browser cannot see, above all programmatic smooth scrolling,
// which no stylesheet can override once a script has asked for it explicitly.
//
// Named exports only; no default export.

import { browser } from '$app/environment';

/**
 * Whether the user has asked their OS to reduce motion. Returns false when
 * there is no window to ask (SSR, or a browser without matchMedia), which is
 * the safe answer: the caller then behaves exactly as it did before.
 */
export function prefersReducedMotion(): boolean {
	if (!browser || typeof window.matchMedia !== 'function') return false;
	return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/**
 * The scroll behaviour to pass to scrollTo/scrollIntoView. Smooth scrolling is
 * a documented vestibular trigger — a long animated jump to the top of an
 * article is exactly the motion those users disabled system-wide.
 */
export function scrollBehavior(): 'auto' | 'smooth' {
	return prefersReducedMotion() ? 'auto' : 'smooth';
}
