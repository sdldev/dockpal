// Svelte stores for global app state

import { writable, derived } from 'svelte/store';
import { api } from './api/client';
import type { User, Template, ContainerInfo, InstanceListItem, SystemInfo, FleetSummary } from './types/api';

export const currentUser = writable<User | null>(null);

// Role helpers — backend roles: admin > operator > viewer
export const userRole = derived(currentUser, ($user) => $user?.role ?? 'viewer');
export const isAdmin = derived(userRole, ($role) => $role === 'admin');
export const isOperator = derived(userRole, ($role) => $role === 'admin' || $role === 'operator');

// Instance selection (defaults to localStorage or 'local')
export const selectedInstance = writable<string>(
	typeof localStorage !== 'undefined'
		? localStorage.getItem('dockpal_selected_instance') || 'local'
		: 'local'
);
// Write-through so every set() persists (legacy parity: state.js selectInstance)
selectedInstance.subscribe((id) => {
	if (typeof localStorage !== 'undefined' && id) {
		localStorage.setItem('dockpal_selected_instance', id);
	}
});

// Current page routing
export const currentPage = writable<string>('servers');

// Selected compose stack name (stacks ↔ compose page navigation)
export const currentStackName = writable<string | null>(null);

// Sidebar visibility. Desktop toggles it fully off (hamburger brings it
// back); on mobile it slides in as an overlay from the hamburger button.
// Default open on desktop (window.innerWidth evaluated once at app start,
// not at module import, so the measurement happens after hydration).
export const sidebarOpen = writable<boolean>(true);

// Current page title shown in the navheader (set by each page, or derived
// from the route id when a page doesn't set one).
export const navTitle = writable<string>('Dockpal');

// Server status summary for the navheader right side (instance + docker).
export interface NavServerStatus {
	hostname: string;
	os: string;
	dockerVersion: string;
	cpuCores: number;
	online: boolean;
}
export const navServerStatus = writable<NavServerStatus | null>(null);

// System-update summary for the sidebar footer (current version + update
// availability). Polled by the Sidebar at a slow cadence; null means "no
// update info yet" and renders just the version placeholder.
export interface SystemUpdateBadge {
	currentVersion?: string;
	updateAvailable: boolean;
	latestVersion?: string;
}
export const systemUpdateBadge = writable<SystemUpdateBadge | null>(null);

// Initial tab for the Containers page, set when arriving via the legacy
// `/images` alias so old bookmarks land on the Images tab (not the default
// Containers tab). Consumed and cleared by ContainersPage on mount.
export const containersInitialTab = writable<'containers' | 'images' | null>(null);

// Initial tab for the server detail page (/servers/:id), set when the
// Servers-table security badge or row menu jumps straight to the Security
// tab. Consumed and reset to 'overview' by ServerDetailPage on mount.
export const serverDetailTab = writable<'overview' | 'security'>('overview');

// Template staged for a new stack (Stacks → Catalog → "New stack from this").
// ComposePage consumes and clears it in add-mode.
export const pendingTemplate = writable<Template | null>(null);

// Templates cache
export const templates = writable<Template[]>([]);

// Notifications/toasts
export const toasts = writable<Array<{ id: string; message: string; type: 'success' | 'error' | 'info' }>>([]);
let toastCounter = 0;

export function addToast(message: string, type: 'success' | 'error' | 'info' = 'info'): void {
	const id = `toast-${++toastCounter}`;
	toasts.update((list) => [...list, { id, message, type }]);
	setTimeout(() => removeToast(id), 4000);
}

export function removeToast(id: string): void {
	toasts.update((list) => list.filter((t) => t.id !== id));
}

// --- Servers dashboard (multi-instance monitoring) ---

export interface ServersInstance extends InstanceListItem {
	sysInfo: SystemInfo | null;
	containers: ContainerInfo[];
	imageCount: number;
}

export interface ServersContainer extends ContainerInfo {
	instanceId: string;
	instanceName: string;
}

export interface ServersState {
	instances: ServersInstance[];
	containers: ServersContainer[];
	loading: boolean;
}

export function createServersStore() {
	let intervalId: ReturnType<typeof setInterval> | null = null;
	const { subscribe, set, update } = writable<ServersState>({
		instances: [],
		containers: [],
		loading: true
	});

	// True when the instance accepts agent calls (local daemon or online agent).
	function isOnline(inst: InstanceListItem): boolean {
		return inst.id === 'local' || inst.status === 'online';
	}

	// One aggregated poll: the backend collects sysInfo, containers and image
	// count for every reachable instance (bounded fan-out server-side) and
	// returns them in a single response. This replaced the 3-requests-per-
	// instance fan-out that tripped the read rate limit with a full fleet.
	async function fetchMetrics() {
		try {
			const summary = await api.get<FleetSummary>('/fleet/summary');
			const resolved: ServersInstance[] = summary.instances.map((row) => ({
				...row,
				sysInfo: row.sys_info,
				containers: row.containers,
				imageCount: row.image_count
			}));

			const allContainers: ServersContainer[] = [];
			for (const inst of resolved) {
				for (const c of inst.containers ?? []) {
					allContainers.push({
						...c,
						instanceId: inst.id,
						instanceName: inst.id === 'local' ? 'This Server' : inst.name
					});
				}
			}

			set({ instances: resolved, containers: allContainers, loading: false });
		} catch (e) {
			console.error('Failed to load servers metrics:', e);
			update((s) => ({ ...s, loading: false }));
		}
	}

	function startPolling(intervalMs = 15000) {
		if (intervalId) return;
		fetchMetrics();
		intervalId = setInterval(fetchMetrics, intervalMs);
	}

	function stopPolling() {
		if (intervalId) {
			clearInterval(intervalId);
			intervalId = null;
		}
	}

	return { subscribe, fetchMetrics, startPolling, stopPolling, isOnline };
}

// Singleton: the servers page, its summary cards and the bulk-deploy tab all
// read the same fleet poll — a second instance would run its own poll loop.
export const servers = createServersStore();
