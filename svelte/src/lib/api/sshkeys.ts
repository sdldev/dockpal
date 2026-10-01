// Saved SSH keys client (Settings → Administration → SSH Keys).
//
// Two flavors exist:
//   - public keys (secret_type "public"): the operator's own public keys
//     (id_ed25519.pub / id_rsa.pub). They are installed into servers'
//     authorized_keys when hardening — the private half stays on the PC.
//   - legacy private keys (secret_type "private"): uploaded before public
//     keys existed so the panel could authenticate as the operator during
//     agent installs. Upload is no longer possible; existing entries keep
//     working for instances that reference them.

import { api } from './client';

export interface SSHKeyInfo {
	id: string;
	name: string;
	fingerprint: string;
	key_type: string;
	/** "public" | "private"; legacy entries may return "" (treat as private). */
	secret_type?: string;
	/** authorized_keys line — public-type keys only. */
	public_key?: string;
	created_at: number;
}

/** List saved SSH keys (admin only). */
export async function listSSHKeys(): Promise<SSHKeyInfo[]> {
	const res = await api.get<SSHKeyInfo[]>('/ssh-keys');
	return Array.isArray(res) ? res : [];
}

/** Upload a PUBLIC key (e.g. the content of ~/.ssh/id_ed25519.pub). */
export async function createSSHKey(name: string, publicKey: string): Promise<SSHKeyInfo> {
	return api.post<SSHKeyInfo>('/ssh-keys', { name, public_key: publicKey });
}

/** Delete a saved key. Instances already installed keep their own copy. */
export async function deleteSSHKey(id: string): Promise<void> {
	await api.delete(`/ssh-keys/${encodeURIComponent(id)}`);
}
