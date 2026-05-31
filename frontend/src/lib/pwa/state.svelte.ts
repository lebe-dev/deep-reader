// Reactive PWA update state — shared between bootstrap.ts and UpdateBanner.svelte.

let _updateAvailable = $state(false);
let _waitingWorker: ServiceWorker | null = null;

export const pwaState = {
	get updateAvailable() {
		return _updateAvailable;
	}
};

export function signalUpdateAvailable(worker: ServiceWorker): void {
	_waitingWorker = worker;
	_updateAvailable = true;
}

export function dismissUpdate(): void {
	_updateAvailable = false;
}

export function applyUpdate(): void {
	if (!_waitingWorker) return;
	_waitingWorker.postMessage({ type: 'SKIP_WAITING' });
	navigator.serviceWorker.addEventListener('controllerchange', () => location.reload());
}
