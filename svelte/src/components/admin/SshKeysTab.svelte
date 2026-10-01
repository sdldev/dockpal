<script lang="ts">
	// SSH Keys tab (Administration): upload private keys once so they can be
	// picked by name when adding servers, instead of pasting key material on
	// every install. Keys are encrypted at rest; only fingerprints are listed.
	import { createSSHKey, deleteSSHKey, listSSHKeys, type SSHKeyInfo } from '$lib/api/sshkeys';
	import { addToast } from '$lib/store';
	import Button from '../ui/Button.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';

	let keys = $state<SSHKeyInfo[]>([]);
	let loading = $state(true);
	let error = $state('');

	let name = $state('');
	let privateKey = $state('');
	let uploading = $state(false);
	let formError = $state('');
	let showForm = $state(false);

	let pendingDelete = $state<SSHKeyInfo | null>(null);
	let deleting = $state(false);

	async function load() {
		loading = true;
		error = '';
		try {
			keys = await listSSHKeys();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load SSH keys';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	async function upload() {
		formError = '';
		if (!name.trim()) {
			formError = 'Name is required';
			return;
		}
		if (!privateKey.trim()) {
			formError = 'Private key is required';
			return;
		}
		uploading = true;
		try {
			await createSSHKey(name.trim(), privateKey);
			addToast(`SSH key "${name.trim()}" saved`, 'success');
			name = '';
			privateKey = '';
			selectedFileName = '';
			showForm = false;
			await load();
		} catch (e) {
			formError = e instanceof Error ? e.message : 'Failed to save key';
		} finally {
			uploading = false;
		}
	}

	// File-based alternative to pasting: read the chosen key file locally and
	// drop its content into the textarea (nothing is sent until "Save key").
	let fileInput = $state<HTMLInputElement | undefined>(undefined);
	let selectedFileName = $state('');

	function pickFile() {
		fileInput?.click();
	}

	function onFileChosen(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		formError = '';
		const reader = new FileReader();
		reader.onload = () => {
			privateKey = String(reader.result ?? '');
			selectedFileName = file.name;
			if (!name.trim()) {
				// Pre-fill a sensible name from the file (id_rsa → "id_rsa").
				name = file.name.replace(/\.(pem|key|rsa)$/, '');
			}
		};
		reader.onerror = () => {
			formError = 'Could not read the selected file';
		};
		reader.readAsText(file);
		// Allow picking the same file again after clearing the form.
		input.value = '';
	}

	async function removeKey() {
		if (!pendingDelete) return;
		deleting = true;
		try {
			await deleteSSHKey(pendingDelete.id);
			addToast(`SSH key "${pendingDelete.name}" deleted`, 'success');
			pendingDelete = null;
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
		} finally {
			deleting = false;
		}
	}

	function formatDate(unix: number) {
		return new Date(unix * 1000).toLocaleDateString();
	}
</script>

<div class="space-y-4">
	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	<div class="flex justify-between items-center">
		<p class="text-xs text-zinc-500">
			Upload private keys used to install the agent on new servers. Keys are encrypted at rest; only the fingerprint is shown.
		</p>
		<Button variant="primary" size="sm" onclick={() => (showForm = !showForm)}>
			{showForm ? 'Cancel' : '+ Upload key'}
		</Button>
	</div>

	{#if showForm}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-3">
			<div>
				<label for="sshkey-name" class="block text-xs font-medium text-zinc-400 mb-1">Name</label>
				<input
					id="sshkey-name"
					type="text"
					bind:value={name}
					placeholder="indatech-PC main key"
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
				/>
			</div>
			<div>
				<div class="flex items-center justify-between mb-1">
					<label for="sshkey-material" class="block text-xs font-medium text-zinc-400">Private key</label>
					<button
						type="button"
						onclick={pickFile}
						class="text-xs text-blue-400 hover:text-blue-300 transition-colors"
					>
						{selectedFileName ? `📄 ${selectedFileName} (change)` : '📄 Upload file'}
					</button>
					<input
						type="file"
						bind:this={fileInput}
						onchange={onFileChosen}
						class="hidden"
						accept=".pem,.key,.rsa,.openssh,id_rsa,id_ed25519"
					/>
				</div>
				<textarea
					id="sshkey-material"
					bind:value={privateKey}
					rows="8"
					spellcheck="false"
					placeholder={'Paste the key, or click "Upload file" above\n-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----'}
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs font-mono text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
				></textarea>
			</div>
			{#if formError}
				<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">{formError}</div>
			{/if}
			<div class="flex justify-end">
				<Button variant="primary" size="sm" loading={uploading} onclick={upload}>Save key</Button>
			</div>
		</div>
	{/if}

	{#if loading && !keys}
		<p class="text-sm text-zinc-500 text-center py-8">Loading SSH keys...</p>
	{:else if keys.length === 0}
		<p class="text-sm text-zinc-500 text-center py-8">No saved keys yet. Upload one to reuse it when adding servers.</p>
	{:else}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b border-zinc-800 text-xs text-zinc-500 text-left">
						<th class="px-4 py-2.5 font-medium">Name</th>
						<th class="px-4 py-2.5 font-medium">Type</th>
						<th class="px-4 py-2.5 font-medium">Fingerprint</th>
						<th class="px-4 py-2.5 font-medium">Added</th>
						<th class="px-4 py-2.5"></th>
					</tr>
				</thead>
				<tbody>
					{#each keys as key (key.id)}
						<tr class="border-b border-zinc-800/50 last:border-0">
							<td class="px-4 py-2.5 text-white">{key.name}</td>
							<td class="px-4 py-2.5 text-zinc-400 font-mono text-xs">{key.key_type}</td>
							<td class="px-4 py-2.5 text-zinc-400 font-mono text-xs truncate max-w-56" title={key.fingerprint}>{key.fingerprint}</td>
							<td class="px-4 py-2.5 text-zinc-500 text-xs">{formatDate(key.created_at)}</td>
							<td class="px-4 py-2.5 text-right">
								<Button variant="danger" size="sm" onclick={() => (pendingDelete = key)}>Delete</Button>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

<ConfirmDialog
	open={pendingDelete !== null}
	title="Delete SSH key"
	message={`Delete saved key "${pendingDelete?.name ?? ''}" (${pendingDelete?.fingerprint ?? ''})? Servers already installed with it are not affected.`}
	confirmLabel="Delete"
	busy={deleting}
	onconfirm={removeKey}
	onclose={() => (pendingDelete = null)}
/>
