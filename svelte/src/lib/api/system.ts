// System self-update client for the /system/update endpoints.
//
// The panel cannot replace its own binary (it runs as the locked-down dockpal
// user), so an admin's "update" request only stages a trigger file that a
// root-owned systemd updater unit consumes. The status polling here is how the
// UI watches that out-of-band update complete across the panel's own restart.

import { api } from './client';

export type UpdateState = 'none' | 'requested' | 'running' | 'done' | 'failed';

export interface UpdateStateDetail {
	status: UpdateState;
	target?: string;
	requested_by?: string;
	requested_at?: number;
	finished_at?: number;
	message?: string;
}

export interface UpdateStatus {
	current_version: string;
	latest_version?: string;
	update_available: boolean;
	update_enabled: boolean;
	last_checked_at?: number;
	changelog?: string;
	changelog_url?: string;
	published_at?: number;
	state: UpdateState;
	state_detail: UpdateStateDetail;
}

/** Fetch the current system-update status (any authenticated user). */
export async function getUpdateStatus(): Promise<UpdateStatus> {
	return api.get<UpdateStatus>('/system/update/status');
}

/** Force a fresh check against the upstream releases (admin only). */
export async function checkForUpdate(): Promise<UpdateStatus> {
	return api.post<UpdateStatus>('/system/update/check');
}

/** Request an update to the given version (admin only). Returns 202. */
export async function requestUpdate(version: string): Promise<{ status: string; target: string }> {
	return api.post<{ status: string; target: string }>('/system/update', { version });
}
