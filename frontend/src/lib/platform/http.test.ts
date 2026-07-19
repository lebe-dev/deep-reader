import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// ---------------------------------------------------------------------------
// The native transport adapter is the highest-risk seam in the mobile port
// (MOBILE-ARCH.md §15): CapacitorHttp must present a fetch-compatible surface so
// api.ts's status/OfflineError/abort handling keeps working unchanged. These
// tests pin the RequestInit -> HttpOptions mapping, the response mapping, and
// the network-failure / abort classification.
//
// We mock ./index (isNative) to switch branches and @capacitor/core to stand in
// for the native plugin — the same mock covers http.ts's dynamic import.
// ---------------------------------------------------------------------------

const native = { value: true };
vi.mock('./index', () => ({
	isNative: () => native.value,
	platform: () => (native.value ? 'ios' : 'web')
}));

const requestMock = vi.fn();
vi.mock('@capacitor/core', () => ({
	Capacitor: { isNativePlatform: () => native.value, getPlatform: () => 'ios' },
	CapacitorHttp: { request: (...args: unknown[]) => requestMock(...args) }
}));

import { httpRequest } from './http';

beforeEach(() => {
	native.value = true;
	requestMock.mockReset();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('web branch', () => {
	it('delegates to global fetch and does not touch CapacitorHttp', async () => {
		native.value = false;
		const fetchMock = vi.fn(async () => ({ ok: true, status: 200 }));
		vi.stubGlobal('fetch', fetchMock);

		await httpRequest('/api/config', { method: 'GET' });

		expect(fetchMock).toHaveBeenCalledWith('/api/config', { method: 'GET' });
		expect(requestMock).not.toHaveBeenCalled();
	});
});

describe('native branch — request mapping', () => {
	it('maps method/headers and parses the JSON string body into data', async () => {
		requestMock.mockResolvedValue({ status: 200, data: '{}' });

		await httpRequest('https://api.example/api/login', {
			method: 'post',
			headers: { 'Content-Type': 'application/json', Authorization: 'Bearer t' },
			body: JSON.stringify({ username: 'u', password: 'p' })
		});

		expect(requestMock).toHaveBeenCalledTimes(1);
		const opts = requestMock.mock.calls[0][0];
		expect(opts.url).toBe('https://api.example/api/login');
		expect(opts.method).toBe('POST');
		expect(opts.headers).toMatchObject({ Authorization: 'Bearer t' });
		// The string body is parsed so CapacitorHttp serialises it per Content-Type.
		expect(opts.data).toEqual({ username: 'u', password: 'p' });
		expect(opts.responseType).toBe('text');
	});

	it('sends no data for a body-less request', async () => {
		requestMock.mockResolvedValue({ status: 200, data: '{}' });
		await httpRequest('https://api.example/api/config', { method: 'GET' });
		expect(requestMock.mock.calls[0][0].data).toBeUndefined();
	});

	it('forwards the headers record verbatim', async () => {
		requestMock.mockResolvedValue({ status: 200, data: '{}' });
		await httpRequest('https://api.example/x', {
			method: 'GET',
			headers: { Accept: 'application/json' }
		});
		expect(requestMock.mock.calls[0][0].headers).toMatchObject({ Accept: 'application/json' });
	});
});

describe('native branch — response mapping', () => {
	it('exposes ok/status and text() for a 2xx JSON string body', async () => {
		requestMock.mockResolvedValue({ status: 200, data: '{"token":"abc"}' });
		const res = await httpRequest('https://api.example/api/login', { method: 'POST', body: '{}' });
		expect(res.ok).toBe(true);
		expect(res.status).toBe(200);
		expect(await res.text()).toBe('{"token":"abc"}');
	});

	it('re-serialises an already-parsed object body so text() stays stable', async () => {
		// Some plugin builds auto-decode JSON despite responseType 'text'.
		requestMock.mockResolvedValue({ status: 200, data: { token: 'abc' } });
		const res = await httpRequest('https://api.example/x', { method: 'GET' });
		expect(await res.text()).toBe('{"token":"abc"}');
	});

	it('does NOT throw on 4xx/5xx — returns ok:false with the status (fetch parity)', async () => {
		requestMock.mockResolvedValue({ status: 422, data: 'bad input' });
		const res = await httpRequest('https://api.example/x', { method: 'GET' });
		expect(res.ok).toBe(false);
		expect(res.status).toBe(422);
		expect(await res.text()).toBe('bad input');
	});

	it('yields an empty string for an empty body so api.ts returns undefined', async () => {
		requestMock.mockResolvedValue({ status: 200, data: '' });
		const res = await httpRequest('https://api.example/x', { method: 'GET' });
		expect(await res.text()).toBe('');
	});
});

describe('native branch — failure classification', () => {
	it('propagates a network rejection unchanged (api.ts maps it to OfflineError)', async () => {
		const netErr = new Error('Network request failed');
		requestMock.mockRejectedValue(netErr);
		await expect(httpRequest('https://api.example/x', { method: 'GET' })).rejects.toBe(netErr);
	});

	it('throws an AbortError synchronously when the signal is already aborted', async () => {
		const ctrl = new AbortController();
		ctrl.abort();
		const err = await httpRequest('https://api.example/x', {
			method: 'GET',
			signal: ctrl.signal
		}).catch((e) => e);
		expect(err).toBeInstanceOf(DOMException);
		expect((err as DOMException).name).toBe('AbortError');
		expect(requestMock).not.toHaveBeenCalled();
	});

	it('rejects with an AbortError when the signal fires mid-flight', async () => {
		const ctrl = new AbortController();
		// Never resolves, so the abort race is what settles the promise.
		requestMock.mockReturnValue(new Promise(() => {}));
		const p = httpRequest('https://api.example/x', { method: 'GET', signal: ctrl.signal });
		ctrl.abort();
		const err = await p.catch((e) => e);
		expect((err as DOMException).name).toBe('AbortError');
	});
});
