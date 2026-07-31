// Unit tests for the device-tilt reader. The point of these is the MATH: the
// first implementation naively used `gamma` alone, which is wrong twice over —
// it ignores that the page renders in screen coordinates while the sensor
// reports device ones, and it spins on noise when the phone lies flat.
//
// As in store.test.ts, the standalone vitest config does not load the Svelte
// compiler, so `$state` is shimmed as identity.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

let browserFlag = true;
vi.mock('$app/environment', () => ({
	get browser() {
		return browserFlag;
	}
}));

type TiltModule = typeof import('./device-tilt.svelte');

/** Captured window listeners, so a test can fire orientation events. */
type Listeners = Record<string, Array<(e: unknown) => void>>;

let listeners: Listeners;
let documentListeners: Listeners;
let screenOrientationAngle: number;
let reduceMotion: boolean;
/** What the iOS permission prompt resolves to, for the gated-platform tests. */
let permissionAnswer: Promise<string>;

/**
 * Build the globals for one platform.
 *
 * `gatedPermission` is the iOS 13+ shape: DeviceOrientationEvent carries a
 * requestPermission method. Android/Chrome does NOT have it and must simply
 * start listening — that difference is the whole platform split, so both sides
 * of it are tested.
 */
function stubEnvironment(options: { hasSensor?: boolean; gatedPermission?: boolean } = {}): void {
	const { hasSensor = true, gatedPermission = false } = options;
	listeners = {};
	screenOrientationAngle = 0;
	reduceMotion = false;
	permissionAnswer = Promise.resolve('granted');

	const win: Record<string, unknown> = {
		addEventListener(type: string, fn: (e: unknown) => void) {
			(listeners[type] ??= []).push(fn);
		},
		removeEventListener(type: string, fn: (e: unknown) => void) {
			listeners[type] = (listeners[type] ?? []).filter((f) => f !== fn);
		},
		matchMedia: () => ({ matches: reduceMotion }),
		get screen() {
			return {
				orientation: {
					get angle() {
						return screenOrientationAngle;
					}
				}
			};
		}
	};
	if (hasSensor) {
		const ctor = function () {} as unknown as Record<string, unknown>;
		if (gatedPermission) ctor.requestPermission = () => permissionAnswer;
		win.DeviceOrientationEvent = ctor;
	}

	vi.stubGlobal('window', win);
	vi.stubGlobal('screen', win.screen);
	documentListeners = {};
	vi.stubGlobal('document', {
		addEventListener(type: string, fn: (e: unknown) => void) {
			(documentListeners[type] ??= []).push(fn);
		},
		removeEventListener(type: string, fn: (e: unknown) => void) {
			documentListeners[type] = (documentListeners[type] ?? []).filter((f) => f !== fn);
		}
	});
	if (hasSensor) vi.stubGlobal('DeviceOrientationEvent', win.DeviceOrientationEvent);
	vi.stubGlobal('$state', <T>(v: T): T => v);
}

async function loadModule(): Promise<TiltModule> {
	vi.resetModules();
	return import('./device-tilt.svelte');
}

/**
 * Fire an orientation event repeatedly so the low-pass filter converges, then
 * report the settled roll. Smoothing is an implementation detail the assertions
 * should not have to model.
 */
function settle(beta: number, gamma: number): void {
	for (let i = 0; i < 200; i++) {
		for (const fn of listeners['deviceorientation'] ?? []) fn({ beta, gamma });
	}
}

beforeEach(() => {
	browserFlag = true;
	stubEnvironment();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('deviceTilt', () => {
	it('is level for a phone held upright in portrait', async () => {
		const { startDeviceTilt, deviceTilt } = await loadModule();
		startDeviceTilt();

		// beta 90 = standing upright facing the user, gamma 0 = no roll.
		settle(90, 0);

		expect(deviceTilt.active).toBe(true);
		expect(Math.abs(deviceTilt.roll)).toBeLessThan(1);
	});

	it('tilts when the device rolls but the UI does not follow', async () => {
		const { startDeviceTilt, deviceTilt } = await loadModule();
		startDeviceTilt();

		// Rolled left with the UI locked to portrait: gravity now pulls toward the
		// side of the screen, so the water must visibly lean.
		settle(45, -60);
		const leftRoll = deviceTilt.roll;
		expect(Math.abs(leftRoll)).toBeGreaterThan(10);

		// Rolling the other way leans it the other way.
		settle(45, 60);
		expect(Math.sign(deviceTilt.roll)).toBe(-Math.sign(leftRoll));
	});

	it('stays level when the phone AND the UI both rotate to landscape', async () => {
		const { startDeviceTilt, deviceTilt } = await loadModule();
		startDeviceTilt();

		// Turned on its side, with the page rotated to match. Nothing moved
		// relative to what the user is looking at, so nothing should move on
		// screen. Reading `gamma` alone — the original bug — would tilt here.
		screenOrientationAngle = 90;
		settle(0, -90);

		expect(Math.abs(deviceTilt.roll)).toBeLessThan(2);
	});

	it('does not spin on noise when the device lies flat', async () => {
		const { startDeviceTilt, deviceTilt } = await loadModule();
		startDeviceTilt();

		// Face-up on a table: gravity is perpendicular to the screen, so the
		// in-plane direction is meaningless and the effect must fade out.
		settle(0, 0);
		expect(Math.abs(deviceTilt.roll)).toBeLessThan(1);

		// A degree of sensor wobble must not produce a visible swing.
		settle(1.5, -1.5);
		expect(Math.abs(deviceTilt.roll)).toBeLessThan(3);
	});

	it('clamps an extreme angle instead of turning the water upside down', async () => {
		const { startDeviceTilt, deviceTilt } = await loadModule();
		startDeviceTilt();

		settle(10, -90);
		expect(Math.abs(deviceTilt.roll)).toBeLessThanOrEqual(30);
	});

	it('ignores readings with no sensor data rather than snapping to level', async () => {
		const { startDeviceTilt, deviceTilt } = await loadModule();
		startDeviceTilt();

		settle(45, -60);
		const before = deviceTilt.roll;

		for (const fn of listeners['deviceorientation'] ?? []) fn({ beta: null, gamma: null });

		expect(deviceTilt.roll).toBe(before);
	});

	it('reports unsupported and stays at zero without the API', async () => {
		stubEnvironment({ hasSensor: false });
		const { startDeviceTilt, deviceTilt } = await loadModule();

		const stop = startDeviceTilt();

		expect(deviceTilt.permission).toBe('unsupported');
		expect(deviceTilt.roll).toBe(0);
		expect(listeners['deviceorientation']).toBeUndefined();
		stop();
	});

	it('does nothing when the user asked for reduced motion', async () => {
		const { startDeviceTilt, deviceTilt } = await loadModule();
		reduceMotion = true;

		startDeviceTilt();

		expect(deviceTilt.permission).toBe('unsupported');
		expect(listeners['deviceorientation']).toBeUndefined();
	});

	// ── Platform split ────────────────────────────────────────────────────────

	it('listens immediately on Android, where there is no permission gate', async () => {
		// Chrome on Android exposes no requestPermission: waiting for one would
		// mean the effect never starts at all on the platform that needs no prompt.
		const { startDeviceTilt, deviceTilt } = await loadModule();

		startDeviceTilt();

		expect(listeners['deviceorientation']).toHaveLength(1);
		expect(deviceTilt.permission).toBe('granted');
		// No gesture listener is registered, because none is needed.
		expect(documentListeners['click']).toBeUndefined();
	});

	it('defers to a user gesture on iOS, then listens once permission is granted', async () => {
		stubEnvironment({ gatedPermission: true });
		const { startDeviceTilt, deviceTilt } = await loadModule();

		startDeviceTilt();

		// Nothing yet: iOS only allows the prompt from inside a gesture handler.
		expect(listeners['deviceorientation']).toBeUndefined();
		expect(documentListeners['click']).toHaveLength(1);

		await documentListeners['click'][0](new Event('click'));

		expect(listeners['deviceorientation']).toHaveLength(1);
		expect(deviceTilt.permission).toBe('granted');
	});

	it('stays level when iOS permission is denied', async () => {
		stubEnvironment({ gatedPermission: true });
		permissionAnswer = Promise.resolve('denied');
		const { startDeviceTilt, deviceTilt } = await loadModule();

		startDeviceTilt();
		await documentListeners['click'][0](new Event('click'));

		expect(listeners['deviceorientation']).toBeUndefined();
		expect(deviceTilt.permission).toBe('denied');
		expect(deviceTilt.roll).toBe(0);
	});

	it('survives a rejected permission request', async () => {
		stubEnvironment({ gatedPermission: true });
		permissionAnswer = Promise.reject(new Error('NotAllowedError'));
		const { startDeviceTilt, deviceTilt } = await loadModule();

		startDeviceTilt();
		await documentListeners['click'][0](new Event('click'));

		expect(deviceTilt.permission).toBe('denied');
		expect(deviceTilt.roll).toBe(0);
	});

	it('detaches only once the last subscriber stops', async () => {
		const { startDeviceTilt } = await loadModule();

		const stopA = startDeviceTilt();
		const stopB = startDeviceTilt();
		expect(listeners['deviceorientation']).toHaveLength(1);

		stopA();
		expect(listeners['deviceorientation']).toHaveLength(1);

		stopB();
		expect(listeners['deviceorientation']).toHaveLength(0);
	});
});
