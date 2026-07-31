/**
 * Helpers shared by the publish dialog, the Public Pages settings tab and the
 * globe affordance in the library / reader.
 *
 * Kept apart from the components so the wording of "expires in …" — the one
 * thing a user checks before handing a link out — is defined once and tested.
 */

/** Lower bound the server accepts for a non-zero link lifetime, in hours. */
export const MIN_TTL_HOURS = 1;

/** Upper bound the server accepts for a link lifetime, in hours (one year). */
export const MAX_TTL_HOURS = 8760;

/** Formats a lifetime in hours as a short human phrase ("3 days", "12 hours"). */
export function formatTtl(hours: number): string {
	if (hours <= 0) return 'never';
	if (hours < 24) return plural(hours, 'hour');

	const days = Math.round(hours / 24);
	if (days < 7) return plural(days, 'day');
	if (days < 31) return plural(Math.round(days / 7), 'week');
	if (days < 365) return plural(Math.round(days / 30), 'month');
	return plural(Math.round(days / 365), 'year');
}

/**
 * Describes when a published link stops working, relative to `now`.
 *
 * Returns `null` for a link with no expiry, and reports an already-elapsed
 * deadline as expired rather than as a negative duration.
 */
export function formatExpiry(expiresAt: string | undefined, now: Date = new Date()): string | null {
	if (!expiresAt) return null;

	const deadline = new Date(expiresAt);
	if (Number.isNaN(deadline.getTime())) return null;

	const hours = (deadline.getTime() - now.getTime()) / 3_600_000;
	if (hours <= 0) return 'Expired';
	if (hours < 1) return `Expires in ${plural(Math.max(1, Math.round(hours * 60)), 'minute')}`;
	return `Expires in ${formatTtl(Math.round(hours))}`;
}

function plural(n: number, unit: string): string {
	return `${n} ${unit}${n === 1 ? '' : 's'}`;
}
