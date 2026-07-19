import { describe, it, expect, vi, beforeEach } from 'vitest';

// Native-branch coverage for the network (§6.3) and lifecycle (§6.4) triggers:
// the plugin listener is registered asynchronously and must be torn down through
// the resolved handle. The web branches are already exercised by store.test.ts
// (window online/offline/focus listeners).

vi.mock('$app/environment', () => ({ browser: true }));

const native = { value: true };
vi.mock('./index', () => ({ isNative: () => native.value }));

let netCb: ((s: { connected: boolean }) => void) | undefined;
const netRemove = vi.fn();
const netAddListener = vi.fn((_event: string, cb: (s: { connected: boolean }) => void) => {
	netCb = cb;
	return Promise.resolve({ remove: netRemove });
});
vi.mock('@capacitor/network', () => ({
	Network: {
		addListener: netAddListener,
		getStatus: async () => ({ connected: true })
	}
}));

let appCb: (() => void) | undefined;
const appRemove = vi.fn();
const appAddListener = vi.fn((_event: string, cb: () => void) => {
	appCb = cb;
	return Promise.resolve({ remove: appRemove });
});
vi.mock('@capacitor/app', () => ({
	App: { addListener: appAddListener }
}));

import { isOnline, onNetworkChange } from './network';
import { onForeground } from './lifecycle';

/** Flush the async plugin registration/teardown (rides a dynamic import). */
async function flush(): Promise<void> {
	await new Promise((resolve) => setTimeout(resolve, 0));
	await new Promise((resolve) => setTimeout(resolve, 0));
}

beforeEach(() => {
	native.value = true;
	netCb = undefined;
	appCb = undefined;
	netRemove.mockClear();
	appRemove.mockClear();
	netAddListener.mockClear();
	appAddListener.mockClear();
});

describe('network (native)', () => {
	it('isOnline reflects the plugin status', async () => {
		expect(await isOnline()).toBe(true);
	});

	it('forwards status changes and removes the listener on unsubscribe', async () => {
		const cb = vi.fn();
		const off = onNetworkChange(cb);
		await flush(); // registration rides a dynamic import

		expect(netAddListener).toHaveBeenCalledWith('networkStatusChange', expect.any(Function));
		netCb?.({ connected: false });
		expect(cb).toHaveBeenCalledWith(false);
		netCb?.({ connected: true });
		expect(cb).toHaveBeenCalledWith(true);

		off();
		await flush();
		expect(netRemove).toHaveBeenCalledTimes(1);
	});

	it('does not register if unsubscribed before the import resolves', async () => {
		const off = onNetworkChange(vi.fn());
		off(); // cancel synchronously, before the import .then runs
		await flush();
		expect(netRemove).not.toHaveBeenCalled();
	});
});

describe('lifecycle (native)', () => {
	it('runs the callback on resume and removes the listener on unsubscribe', async () => {
		const cb = vi.fn();
		const off = onForeground(cb);
		await flush();

		expect(appAddListener).toHaveBeenCalledWith('resume', expect.any(Function));
		appCb?.();
		expect(cb).toHaveBeenCalledTimes(1);

		off();
		await flush();
		expect(appRemove).toHaveBeenCalledTimes(1);
	});
});
