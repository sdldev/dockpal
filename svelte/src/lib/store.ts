// Svelte stores for global app state

import { writable, derived } from 'svelte/store';
import { api } from './api/client';
import type { User, Template, ContainerInfo, InstanceListItem, SystemInfo } from './types/api';

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
export const currentPage = writable<string>('dashboard');

// Selected compose stack name (stacks ↔ compose page navigation)
export const currentStackName = writable<string | null>(null);

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

// --- Fleet dashboard (multi-instance monitoring) ---

export interface FleetInstance extends InstanceListItem {
	sysInfo: SystemInfo | null;
	containers: ContainerInfo[];
}

export interface FleetContainer extends ContainerInfo {
	instanceId: string;
	instanceName: string;
}

export interface FleetState {
	instances: FleetInstance[];
	containers: FleetContainer[];
	loading: boolean;
}

export function createFleetStore() {
	let intervalId: ReturnType<typeof setInterval> | null = null;
	const { subscribe, set, update } = writable<FleetState>({
		instances: [],
		containers: [],
		loading: true
	});

	// True when the instance accepts agent calls (local daemon or online agent).
	function isOnline(inst: InstanceListItem): boolean {
		return inst.id === 'local' || inst.status === 'online';
	}

	// Fetch the instance list, then sysInfo + containers for every reachable
	// instance in parallel. Flattens containers into the global fleet view.
	async function fetchMetrics() {
		try {
			const list = await api.get<InstanceListItem[]>('/instances');
			const resolved = await Promise.all(
				list.map(async (inst) => {
					let sysInfo: SystemInfo | null = null;
					let containers: ContainerInfo[] = [];
					if (isOnline(inst)) {
						try {
							sysInfo = await api.get<SystemInfo>(`/instances/${inst.id}/system/info`);
						} catch (e) {
							console.error(`Failed to get system info for instance ${inst.id}:`, e);
						}
						try {
							containers = await api.get<ContainerInfo[]>(`/instances/${inst.id}/containers`);
						} catch (e) {
							console.error(`Failed to get containers for instance ${inst.id}:`, e);
						}
					}
					return { ...inst, sysInfo, containers };
				})
			);

			const allContainers: FleetContainer[] = [];
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
			console.error('Failed to load fleet metrics:', e);
			update((s) => ({ ...s, loading: false }));
		}
	}

	function startPolling(intervalMs = 5000) {
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
