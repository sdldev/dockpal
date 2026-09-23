// Minimal URL router: syncs the `currentPage` store with the browser URL so the
// SPA supports clean deep links (/dashboard, /fleet, /containers/:id) and
// back/forward navigation — replacing the legacy Alpine router.
import { writable } from 'svelte/store';
import { currentPage } from './store';

export interface RouteParams {
	id?: string;
}

export const routeParams = writable<RouteParams>({});

// SPA page id → canonical path.
const pagePaths: Record<string, string> = {
	dashboard: '/dashboard',
	fleet: '/fleet',
	stacks: '/stacks',
	compose: '/compose',
	containers: '/containers',
	'container-detail': '/containers', // dynamic: /containers/:id (see pageToPath)
	images: '/images',
	services: '/services',
	templates: '/templates',
	webhooks: '/webhooks',
	apps: '/apps',
	domains: '/domains',
	settings: '/settings',
	admin: '/admin'
};

// Legacy Alpine paths that map onto SPA pages, so old bookmarks keep working.
const legacyAliases: Record<string, string> = {
	'/profile': 'settings',
	'/registry': 'admin',
	'/deploy': 'templates',
	'/instances': 'fleet',
	'/add-instance': 'fleet'
};

function normalize(path: string): string {
	return path.replace(/^\/+|\/+$/g, '');
}

// Resolve a URL path into a page id + params (container id for detail view).
export function pathToPage(path: string): { page: string; params: RouteParams } {
	const p = normalize(path) || 'dashboard';
	const parts = p.split('/');

	// /containers/:id → container detail
	if (parts[0] === 'containers' && parts.length === 2 && parts[1]) {
		return { page: 'container-detail', params: { id: decodeURIComponent(parts[1]) } };
	}

	const base = '/' + parts[0];
	if (legacyAliases[base]) return { page: legacyAliases[base], params: {} };

	const entry = Object.entries(pagePaths).find(([, p]) => p === base);
	if (entry) return { page: entry[0], params: {} };

	// Unknown path → dashboard (the SPA has no 404 page).
	return { page: 'dashboard', params: {} };
}

// Build the URL for a page (+ params).
export function pageToPath(page: string, params: RouteParams = {}): string {
	if (page === 'container-detail' && params.id) {
		return '/containers/' + encodeURIComponent(params.id);
	}
	return pagePaths[page] ?? '/dashboard';
}

// Navigate to a page: updates state + URL. Pass `replace` to overwrite the
// current history entry (used for logout/redirect-to-default).
export function navigate(page: string, params: RouteParams = {}, replace = false): void {
	currentPage.set(page);
	routeParams.set(params);
	const url = pageToPath(page, params);
	if (window.location.pathname !== url) {
		if (replace) {
			window.history.replaceState({ page, params }, '', url);
		} else {
			window.history.pushState({ page, params }, '', url);
		}
	}
}

function handlePopState(): void {
	const { page, params } = pathToPage(window.location.pathname);
	currentPage.set(page);
	routeParams.set(params);
}

// Read the current URL into the stores and start listening for back/forward.
// Call once on mount in App.svelte.
export function initRouter(): void {
	const { page, params } = pathToPage(window.location.pathname);
	currentPage.set(page);
	routeParams.set(params);
	window.addEventListener('popstate', handlePopState);
}
