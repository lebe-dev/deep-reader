// Device tilt (Svelte 5 runes): where "down" is, relative to the SCREEN, for
// decorative motion such as the water level in the app mark.
//
// Deliberately narrow: one shared listener for the whole app, one number out.
// It is decorative, so every failure mode — no sensor, no permission, a desktop
// browser, prefers-reduced-motion — degrades to a steady 0 rather than an error.
//
// Named exports only; no default export.

import { browser } from '$app/environment';

/** How far the reading is allowed to swing, in degrees. */
const MAX_TILT_DEG = 30;

/**
 * Low-pass smoothing factor per event (0..1]. Raw sensor output is noisy enough
 * that a logo driven straight off it visibly jitters while the phone sits still
 * on a table; this trades a little latency for a surface that settles.
 */
const SMOOTHING = 0.15;

/**
 * Below this share of gravity lying in the screen plane, the direction of "down"
 * on screen is meaningless (the device is flat on a table, gravity points
 * straight through the screen) and its computed angle spins on noise alone. The
 * effect is faded out below the threshold instead.
 */
const FLAT_THRESHOLD = 0.25;

/** Permission state, for callers that want to explain themselves to the user. */
export type TiltPermission = 'unsupported' | 'pending' | 'granted' | 'denied';

interface TiltState {
	/**
	 * Angle of screen-space "down", in degrees, clamped to ±MAX_TILT_DEG.
	 * 0 means gravity points at the bottom of the screen — the normal case for a
	 * phone held upright in any orientation the UI has rotated to match.
	 */
	roll: number;
	/** True once a real orientation reading has arrived. */
	active: boolean;
	permission: TiltPermission;
}

export const deviceTilt = $state<TiltState>({ roll: 0, active: false, permission: 'pending' });

let listening = false;
let refCount = 0;
let pendingGesture = false;

const DEG = Math.PI / 180;

/** Whether the browser exposes the API at all (desktop Chrome does not). */
function supported(): boolean {
	return browser && typeof window !== 'undefined' && 'DeviceOrientationEvent' in window;
}

/** Whether the user asked for reduced motion. */
function reducedMotion(): boolean {
	return browser && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/**
 * How far the UI has been rotated away from the device's native orientation.
 * beta/gamma are reported in DEVICE coordinates, but the page renders in SCREEN
 * coordinates — turning a phone to landscape rotates the second and not the
 * first. Without this term, flipping the phone sideways moves the water even
 * though nothing moved relative to what the user is looking at, and a UI locked
 * to portrait gets no movement where it should get the most.
 */
function screenAngle(): number {
	if (!browser) return 0;
	const angle = window.screen?.orientation?.angle;
	if (typeof angle === 'number') return angle;
	// Older iOS Safari.
	const legacy = (window as unknown as { orientation?: number }).orientation;
	return typeof legacy === 'number' ? legacy : 0;
}

function handleOrientation(event: DeviceOrientationEvent): void {
	const { beta, gamma } = event;
	// A null reading means the platform fired the event without real sensor data
	// — ignore it rather than snapping the water back to level.
	if (beta === null || beta === undefined || gamma === null || gamma === undefined) return;

	// The gravity vector in device coordinates (x right, y up, z out of screen).
	// Only its projection onto the screen plane matters here.
	const b = beta * DEG;
	const g = gamma * DEG;
	const gx = -Math.cos(b) * Math.sin(g);
	const gy = -Math.sin(b);

	// How much of gravity lies in the screen plane: ~1 when the phone is upright,
	// ~0 when it is flat on a table.
	const inPlane = Math.hypot(gx, gy);

	// Angle of screen-space "down" measured from the bottom of the screen, with
	// the UI's own rotation taken out.
	let angle = Math.atan2(gx, -gy) / DEG - screenAngle();
	// Normalise to (-180, 180] so a wrap-around does not send the water spinning
	// the long way round.
	angle = ((((angle + 180) % 360) + 360) % 360) - 180;

	// Fade the effect out as the device goes flat, where `angle` is noise. The
	// falloff is SQUARED rather than linear: a linear one still let a degree or
	// two of sensor wobble on a table-flat phone swing the water several degrees,
	// because the angle it computes there is essentially random.
	const ramp = Math.min(1, inPlane / FLAT_THRESHOLD);
	const strength = ramp * ramp;
	const target = Math.max(-MAX_TILT_DEG, Math.min(MAX_TILT_DEG, angle)) * strength;

	deviceTilt.roll += (target - deviceTilt.roll) * SMOOTHING;
	deviceTilt.active = true;
}

function attach(): void {
	if (listening) return;
	listening = true;
	deviceTilt.permission = 'granted';
	window.addEventListener('deviceorientation', handleOrientation);
}

/**
 * iOS 13+ gates the sensor behind an explicit permission prompt that may only be
 * raised from a user gesture. Requesting on mount would therefore always be
 * rejected, so the request is deferred to the first interaction anywhere in the
 * app — and if the user never taps, or declines, the mark simply stays level.
 */
function requestOnFirstGesture(): void {
	if (pendingGesture) return;
	pendingGesture = true;

	const events = ['click', 'touchend'] as const;
	const ask = async () => {
		for (const name of events) document.removeEventListener(name, ask);
		try {
			// iOS resolves this to 'granted' | 'denied'; typed locally because the
			// DOM lib does not declare the iOS-only method.
			const request = (
				DeviceOrientationEvent as unknown as {
					requestPermission?: () => Promise<string>;
				}
			).requestPermission;
			if (typeof request !== 'function') {
				attach();
				return;
			}
			if ((await request()) !== 'granted') {
				deviceTilt.permission = 'denied';
				return;
			}
			attach();
		} catch {
			// A rejected or unavailable permission is not an error worth reporting:
			// the feature is decoration.
			deviceTilt.permission = 'denied';
		}
	};
	for (const name of events) document.addEventListener(name, ask);
}

/**
 * Start reading the device tilt, returning the matching stop function.
 * Reference-counted, so several components may subscribe and the listener is
 * removed only once the last of them goes away.
 */
export function startDeviceTilt(): () => void {
	if (!supported() || reducedMotion()) {
		deviceTilt.permission = 'unsupported';
		return () => {};
	}

	refCount++;
	const needsPermission =
		typeof (DeviceOrientationEvent as unknown as { requestPermission?: unknown })
			.requestPermission === 'function';

	if (needsPermission) {
		requestOnFirstGesture();
	} else {
		attach();
	}

	return () => {
		refCount--;
		if (refCount > 0 || !listening) return;
		window.removeEventListener('deviceorientation', handleOrientation);
		listening = false;
		deviceTilt.roll = 0;
		deviceTilt.active = false;
	};
}
