import { describe, expect, it } from 'vitest';

import {
	base64UrlToBytes,
	bytesToBase64Url,
	serializeAssertion,
	serializeAttestation,
	toCreationOptions,
	toRequestOptions
} from './webauthn';

/** Build an ArrayBuffer from byte values. */
function buffer(...bytes: number[]): ArrayBuffer {
	return new Uint8Array(bytes).buffer;
}

describe('base64url conversion', () => {
	it('round-trips arbitrary bytes', () => {
		const bytes = new Uint8Array(256);
		for (let i = 0; i < 256; i++) bytes[i] = i;

		const encoded = bytesToBase64Url(bytes);
		expect(base64UrlToBytes(encoded)).toEqual(bytes);
	});

	it('emits the url-safe alphabet without padding', () => {
		// 0xFB 0xFF encodes to "+/8" in the standard alphabet, so this input is
		// exactly the case where the two alphabets differ.
		const encoded = bytesToBase64Url(new Uint8Array([0xfb, 0xff, 0xbf]));
		expect(encoded).not.toMatch(/[+/=]/);
		expect(base64UrlToBytes(encoded)).toEqual(new Uint8Array([0xfb, 0xff, 0xbf]));
	});

	it('decodes padded input too', () => {
		// The server never pads, but a shim or a proxy might; accepting both keeps
		// a working ceremony from failing on a cosmetic difference.
		expect(base64UrlToBytes('AQID')).toEqual(new Uint8Array([1, 2, 3]));
		expect(base64UrlToBytes('AQI=')).toEqual(new Uint8Array([1, 2]));
		expect(base64UrlToBytes('AQ==')).toEqual(new Uint8Array([1]));
	});

	it('decodes an empty string to no bytes', () => {
		expect(base64UrlToBytes('')).toEqual(new Uint8Array([]));
	});
});

describe('toCreationOptions', () => {
	const wire = {
		publicKey: {
			challenge: 'AQIDBA',
			rp: { id: 'reader.example', name: 'Deep Reader' },
			user: { id: 'BQYHCA', name: 'reader', displayName: 'reader' },
			pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
			timeout: 60000,
			authenticatorSelection: { residentKey: 'required', userVerification: 'required' },
			attestation: 'none'
		}
	};

	it('decodes the binary fields and preserves the rest', () => {
		const options = toCreationOptions(wire).publicKey!;

		expect(new Uint8Array(options.challenge as ArrayBuffer)).toEqual(new Uint8Array([1, 2, 3, 4]));
		expect(new Uint8Array(options.user.id as ArrayBuffer)).toEqual(new Uint8Array([5, 6, 7, 8]));
		expect(options.rp).toEqual({ id: 'reader.example', name: 'Deep Reader' });
		expect(options.timeout).toBe(60000);
		expect(options.attestation).toBe('none');
		expect(options.authenticatorSelection).toEqual({
			residentKey: 'required',
			userVerification: 'required'
		});
	});

	it('decodes excludeCredentials ids', () => {
		const options = toCreationOptions({
			publicKey: {
				...wire.publicKey,
				excludeCredentials: [{ type: 'public-key', id: 'AQID', transports: ['internal'] }]
			}
		}).publicKey!;

		expect(options.excludeCredentials).toHaveLength(1);
		expect(new Uint8Array(options.excludeCredentials![0].id as ArrayBuffer)).toEqual(
			new Uint8Array([1, 2, 3])
		);
		expect(options.excludeCredentials![0].transports).toEqual(['internal']);
	});

	it('omits an empty excludeCredentials rather than sending []', () => {
		const options = toCreationOptions({
			publicKey: { ...wire.publicKey, excludeCredentials: [] }
		}).publicKey!;

		expect(options.excludeCredentials).toBeUndefined();
	});

	it('throws on a malformed payload', () => {
		// Failing here beats failing inside the credential API, where the browser's
		// error says nothing about which field was wrong.
		expect(() => toCreationOptions(undefined)).toThrow();
		expect(() => toCreationOptions({})).toThrow();
		expect(() => toCreationOptions({ publicKey: { challenge: 'AQID' } })).toThrow();
	});
});

describe('toRequestOptions', () => {
	it('decodes the challenge and keeps the rp id', () => {
		const options = toRequestOptions({
			publicKey: {
				challenge: 'AQIDBA',
				rpId: 'reader.example',
				userVerification: 'required',
				timeout: 60000
			}
		}).publicKey!;

		expect(new Uint8Array(options.challenge as ArrayBuffer)).toEqual(new Uint8Array([1, 2, 3, 4]));
		expect(options.rpId).toBe('reader.example');
		expect(options.userVerification).toBe('required');
	});

	it('leaves allowCredentials undefined for a discoverable challenge', () => {
		// A discoverable login sends no credential list; passing [] would stop some
		// browsers from offering the resident credential at all.
		const options = toRequestOptions({ publicKey: { challenge: 'AQID' } }).publicKey!;
		expect(options.allowCredentials).toBeUndefined();
	});

	it('throws on a malformed payload', () => {
		expect(() => toRequestOptions(undefined)).toThrow();
		expect(() => toRequestOptions({ publicKey: {} })).toThrow();
	});
});

describe('serializeAttestation', () => {
	it('encodes every binary field as base64url', () => {
		const credential = {
			id: 'cred-id',
			rawId: buffer(1, 2, 3),
			type: 'public-key',
			authenticatorAttachment: 'platform',
			getClientExtensionResults: () => ({ credProps: { rk: true } }),
			response: {
				clientDataJSON: buffer(4, 5, 6),
				attestationObject: buffer(7, 8, 9),
				getTransports: () => ['internal', 'hybrid']
			}
		} as unknown as PublicKeyCredential;

		expect(serializeAttestation(credential)).toEqual({
			id: 'cred-id',
			rawId: 'AQID',
			type: 'public-key',
			authenticatorAttachment: 'platform',
			clientExtensionResults: { credProps: { rk: true } },
			response: {
				clientDataJSON: 'BAUG',
				attestationObject: 'BwgJ',
				transports: ['internal', 'hybrid']
			}
		});
	});

	it('tolerates an authenticator response without getTransports', () => {
		const credential = {
			id: 'cred-id',
			rawId: buffer(1),
			type: 'public-key',
			response: { clientDataJSON: buffer(2), attestationObject: buffer(3) }
		} as unknown as PublicKeyCredential;

		const serialized = serializeAttestation(credential) as { response: { transports: string[] } };
		expect(serialized.response.transports).toEqual([]);
	});
});

describe('serializeAssertion', () => {
	it('includes the user handle that makes the login discoverable', () => {
		const credential = {
			id: 'cred-id',
			rawId: buffer(1, 2, 3),
			type: 'public-key',
			getClientExtensionResults: () => ({}),
			response: {
				clientDataJSON: buffer(4, 5, 6),
				authenticatorData: buffer(7, 8, 9),
				signature: buffer(10, 11, 12),
				userHandle: buffer(13, 14, 15)
			}
		} as unknown as PublicKeyCredential;

		const serialized = serializeAssertion(credential) as {
			response: { userHandle?: string; signature: string };
		};
		expect(serialized.response.userHandle).toBe('DQ4P');
		expect(serialized.response.signature).toBe('CgsM');
	});

	it('omits a null user handle instead of sending an empty string', () => {
		const credential = {
			id: 'cred-id',
			rawId: buffer(1),
			type: 'public-key',
			response: {
				clientDataJSON: buffer(2),
				authenticatorData: buffer(3),
				signature: buffer(4),
				userHandle: null
			}
		} as unknown as PublicKeyCredential;

		const serialized = serializeAssertion(credential) as { response: { userHandle?: string } };
		expect(serialized.response.userHandle).toBeUndefined();
	});
});
