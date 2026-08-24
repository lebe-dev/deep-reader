// WebAuthn wire conversion.
//
// The backend speaks the WebAuthn JSON dialect: every binary field (challenge,
// user handle, credential ids, attestation blobs) travels as unpadded base64url.
// The browser API speaks ArrayBuffers. This module is the single translation
// point between the two, in both directions, and it holds no state — everything
// here is a pure function so it is directly unit-testable.
//
// Keeping the conversion here rather than inline in the pages also means the
// native path gets it for free: `@capgo/capacitor-passkey` installs a shim over
// `navigator.credentials`, so the same buffers-in / buffers-out contract holds
// on iOS and Android.
//
// Named exports only; no default export.

/** Decode unpadded (or padded) base64url into bytes. */
export function base64UrlToBytes(value: string): Uint8Array {
	const normalized = value.replace(/-/g, '+').replace(/_/g, '/');
	// atob requires the padding the wire format omits.
	const padded = normalized.padEnd(normalized.length + ((4 - (normalized.length % 4)) % 4), '=');
	const binary = atob(padded);
	const bytes = new Uint8Array(binary.length);
	for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
	return bytes;
}

/** Encode a buffer as unpadded base64url. */
export function bytesToBase64Url(buffer: ArrayBuffer | Uint8Array): string {
	const bytes = buffer instanceof Uint8Array ? buffer : new Uint8Array(buffer);
	let binary = '';
	for (const byte of bytes) binary += String.fromCharCode(byte);
	return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/**
 * A credential descriptor as it arrives on the wire (`excludeCredentials` /
 * `allowCredentials`), with the id still base64url-encoded.
 */
interface WireCredentialDescriptor {
	type: string;
	id: string;
	transports?: string[];
}

/** `PublicKeyCredentialCreationOptions` as it arrives on the wire. */
interface WireCreationOptions {
	publicKey: {
		challenge: string;
		user: { id: string; name: string; displayName: string };
		excludeCredentials?: WireCredentialDescriptor[];
		[key: string]: unknown;
	};
}

/** `PublicKeyCredentialRequestOptions` as it arrives on the wire. */
interface WireRequestOptions {
	publicKey: {
		challenge: string;
		allowCredentials?: WireCredentialDescriptor[];
		[key: string]: unknown;
	};
}

/**
 * Convert the server's creation options into the shape
 * `navigator.credentials.create()` expects.
 *
 * Throws when the payload is not a creation options object — a server that
 * answered with something unexpected must fail here, loudly, rather than inside
 * the browser's credential API where the error is opaque.
 */
export function toCreationOptions(payload: unknown): CredentialCreationOptions {
	const wire = payload as WireCreationOptions | undefined;
	if (!wire?.publicKey?.challenge || !wire.publicKey.user?.id) {
		throw new Error('Malformed passkey registration options');
	}
	const { challenge, user, excludeCredentials, ...rest } = wire.publicKey;

	return {
		publicKey: {
			...rest,
			challenge: toBuffer(challenge),
			user: { ...user, id: toBuffer(user.id) },
			excludeCredentials: toDescriptors(excludeCredentials)
		} as PublicKeyCredentialCreationOptions
	};
}

/**
 * Convert the server's assertion options into the shape
 * `navigator.credentials.get()` expects.
 */
export function toRequestOptions(payload: unknown): CredentialRequestOptions {
	const wire = payload as WireRequestOptions | undefined;
	if (!wire?.publicKey?.challenge) {
		throw new Error('Malformed passkey sign-in options');
	}
	const { challenge, allowCredentials, ...rest } = wire.publicKey;

	return {
		publicKey: {
			...rest,
			challenge: toBuffer(challenge),
			allowCredentials: toDescriptors(allowCredentials)
		} as PublicKeyCredentialRequestOptions
	};
}

/**
 * Serialize the credential returned by `navigator.credentials.create()` into the
 * JSON the backend parses.
 */
export function serializeAttestation(credential: PublicKeyCredential): unknown {
	const response = credential.response as AuthenticatorAttestationResponse;
	return {
		id: credential.id,
		rawId: bytesToBase64Url(credential.rawId),
		type: credential.type,
		authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
		clientExtensionResults: credential.getClientExtensionResults?.() ?? {},
		response: {
			clientDataJSON: bytesToBase64Url(response.clientDataJSON),
			attestationObject: bytesToBase64Url(response.attestationObject),
			// Optional and not implemented everywhere; the server uses it only as a
			// hint for future sign-in prompts, so a missing value is harmless.
			transports: response.getTransports?.() ?? []
		}
	};
}

/**
 * Serialize the credential returned by `navigator.credentials.get()` into the
 * JSON the backend parses.
 */
export function serializeAssertion(credential: PublicKeyCredential): unknown {
	const response = credential.response as AuthenticatorAssertionResponse;
	return {
		id: credential.id,
		rawId: bytesToBase64Url(credential.rawId),
		type: credential.type,
		authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
		clientExtensionResults: credential.getClientExtensionResults?.() ?? {},
		response: {
			clientDataJSON: bytesToBase64Url(response.clientDataJSON),
			authenticatorData: bytesToBase64Url(response.authenticatorData),
			signature: bytesToBase64Url(response.signature),
			// The user handle is what makes the login discoverable: it is how the
			// server resolves the account without a username. It is null only for
			// non-resident credentials, which this app never registers.
			userHandle: response.userHandle ? bytesToBase64Url(response.userHandle) : undefined
		}
	};
}

/** Decode a wire string into the ArrayBuffer the credential API expects. */
function toBuffer(value: string): ArrayBuffer {
	const bytes = base64UrlToBytes(value);
	// Return the exact slice: a Uint8Array view may sit inside a larger buffer.
	return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
}

/**
 * Decode a credential-descriptor list, returning undefined for an absent or
 * empty one — an empty `allowCredentials` is not the same as omitting it, and
 * passing `[]` disables discoverable selection in some browsers.
 */
function toDescriptors(
	list?: WireCredentialDescriptor[]
): PublicKeyCredentialDescriptor[] | undefined {
	if (!list || list.length === 0) return undefined;
	return list.map((descriptor) => ({
		type: descriptor.type as PublicKeyCredentialType,
		id: toBuffer(descriptor.id),
		transports: descriptor.transports as AuthenticatorTransport[] | undefined
	}));
}
