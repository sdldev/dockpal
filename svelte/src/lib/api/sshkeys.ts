// Saved SSH keys client (Settings → Administration → SSH Keys). Private key
// material is uploaded once, encrypted at rest by the backend, and referenced
// by ID from the Add Server installer — the list endpoints never return the
// secret.

import { api } from './client';

export interface SSHKeyInfo {
	id: string;
	name: string;
	fingerprint: string;
	key_type: string;
	created_at: number;
}

/** List saved SSH keys (admin only). */
export async function listSSHKeys(): Promise<SSHKeyInfo[]> {
	const res = await api.get<SSHKeyInfo[]>('/ssh-keys');
	return Array.isArray(res) ? res : [];
}

/** Upload a private key. The backend validates it parses and stores it encrypted. */
export async function createSSHKey(name: string, privateKey: string): Promise<SSHKeyInfo> {
	return api.post<SSHKeyInfo>('/ssh-keys', { name, private_key: privateKey });
}

/** Delete a saved key. Instances already installed keep their own copy. */
export async function deleteSSHKey(id: string): Promise<void> {
	await api.delete(`/ssh-keys/${encodeURIComponent(id)}`);
}
