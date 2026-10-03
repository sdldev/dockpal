// Minimal URL router: syncs the `currentPage` store with the browser URL so the
// SPA supports clean deep links (/dashboard, /servers, /containers/:id) and
// back/forward navigation — replacing the legacy Alpine router.
import { writable } from 'svelte/store';
import { currentPage, containersInitialTab } from './store';

export interface RouteParams {
	id?: string;
}

export const routeParams = writable<RouteParams>({});

// SPA page id → canonical path.
const pagePaths: Record<string, string> = {
	dashboard: '/dashboard',
	servers: '/servers',
	stacks: '/stacks',
	compose: '/compose',
	containers: '/containers',
	'container-detail': '/containers', // dynamic: /containers/:id (see pageToPath)
	integrations: '/integrations',
	settings: '/settings'
};

// Paths whose pages were folded into other pages by the IA consolidation,
// kept working for old bookmarks: Images → Containers tab, Installed Apps
// (updates) → Stacks tab, Webhooks/Domains → Integrations tabs, Admin →
// Settings tab, App Installer → Stacks catalog, /fleet + /instances → the
// Servers page (renamed in the v1.0.0 IA).
const legacyAliases: Record<string, string> = {
	'/fleet': 'servers',
	'/profile': 'settings',
	'/registry': 'settings',
	'/admin': 'settings',
	'/deploy': 'stacks',
	'/templates': 'stacks',
	'/services': 'stacks',
	'/apps': 'stacks',
	'/images': 'containers',
	'/webhooks': 'integrations',
	'/domains': 'integrations',
	'/instances': 'servers',
	'/add-instance': 'servers'
};

function normalize(path: string): string {
	return path.replace(/^\/+|\/+$/g, '');
}

// Resolve a URL path into a page id + params (container id for detail view).
export function pathToPage(path: string): { page: string; params: RouteParams } {
	// Root path lands on Servers — the natural starting point.
	const p = normalize(path) || 'servers';
	const parts = p.split('/');

	// /containers/:id → container detail
	if (parts[0] === 'containers' && parts.length === 2 && parts[1]) {
		return { page: 'container-detail', params: { id: decodeURIComponent(parts[1]) } };
	}

	const base = '/' + parts[0];
	if (legacyAliases[base]) {
		// The `/images` alias lands on the Containers page; flag it so the page
		// opens on the Images tab rather than the default Containers tab.
		containersInitialTab.set(base === '/images' ? 'images' : null);
		return { page: legacyAliases[base], params: {} };
	}

	const entry = Object.entries(pagePaths).find(([, p]) => p === base);
	if (entry) return { page: entry[0], params: {} };

	// Unknown path → Servers (the SPA has no 404 page).
	return { page: 'servers', params: {} };
}

// Build the URL for a page (+ params).
export function pageToPath(page: string, params: RouteParams = {}): string {
	if (page === 'container-detail' && params.id) {
		return '/containers/' + encodeURIComponent(params.id);
	}
	return pagePaths[page] ?? '/servers';
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
