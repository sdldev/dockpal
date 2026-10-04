<script lang="ts">
  // Per-service card for the compose page — Svelte port of Dockge's
  // Container.vue. Shows runtime info in view mode and GUI form fields in
  // edit mode; edits mutate the bound `service` object, which the parent
  // converts back into YAML (comment-preserving).
  import ArrayInput from './ArrayInput.svelte';
  import ArraySelect from './ArraySelect.svelte';
  import { parseDockerPort, serviceStateColor, RESTART_POLICIES } from '$lib/stack-utils';
  import type { StackService } from '$lib/api/stacks';

  interface Props {
    name: string;
    service: Record<string, any>; // live entry from jsonConfig.services
    envsubstService?: Record<string, any>; // resolved-values view
    isEditMode: boolean;
    serviceStatus?: StackService | null;
    serviceCount: number;
    networkOptions: string[];
    hostname?: string;
    onstart?: () => void;
    onstop?: () => void;
    onrestart?: () => void;
    onrecreate?: () => void;
    onremove?: () => void;
  }

  let {
    name,
    service = $bindable<Record<string, any>>({}),
    envsubstService = {},
    isEditMode,
    serviceStatus = null,
    serviceCount,
    networkOptions,
    hostname = '',
    onstart,
    onstop,
    onrestart,
    onrecreate,
    onremove
  }: Props = $props();

  let showConfig = $state(false);

  // --- network mode (issue #28) ---
  // Compose forbids combining network_mode with networks; host mode also
  // makes port bindings meaningless. The UI picks one of the two surfaces.
  const NETWORK_MODES = ['host', 'none'] as const;
  const networkMode = $derived(
    typeof service.network_mode === 'string' && NETWORK_MODES.includes(service.network_mode as any)
      ? (service.network_mode as string)
      : ''
  );
  const hasNetworkMode = $derived(networkMode !== '');
  const hasCustomNetworkMode = $derived(
    typeof service.network_mode === 'string' && service.network_mode !== '' && networkMode === ''
  );

  function setNetworkMode(mode: string) {
    if (mode === '') {
      delete service.network_mode;
    } else {
      service.network_mode = mode;
      // network_mode and networks are mutually exclusive in compose —
      // drop the attachment list so the generated YAML stays valid.
      delete service.networks;
    }
  }

  const imageFull = $derived((envsubstService?.image ?? service.image ?? '') as string);
  const imageName = $derived(imageFull ? imageFull.split(':')[0] : '');
  const imageTag = $derived(() => {
    if (!imageFull) return '';
    const tag = imageFull.split(':')[1];
    return tag ?? 'latest';
  });

  const svcState = $derived(serviceStatus?.state ?? '');
  const svcHealth = $derived(serviceStatus?.health ?? '');
  const statusDisplay = $derived(svcHealth || svcState || 'N/A');
  const isRunning = $derived(svcState === 'running' || svcHealth === 'healthy');

  function portsList(): Array<{ url: string; display: string }> {
    const ports = (envsubstService?.ports ?? service.ports) as string[] | undefined;
    if (!Array.isArray(ports)) return [];
    return ports
      .filter((p) => typeof p === 'string')
      .map((p) => parseDockerPort(p, hostname || window.location.hostname));
  }
</script>

<div class="rounded-md border border-zinc-700 bg-zinc-800/60 p-4">
  <div class="flex items-start justify-between gap-4">
    <div class="min-w-0">
      <h4 class="truncate text-base font-semibold text-white">{name}</h4>
      {#if imageFull}
        <div class="mt-0.5 text-xs text-zinc-400">
          {imageName}:<span class="text-zinc-300">{imageTag()}</span>
        </div>
      {/if}
      {#if !isEditMode}
        <div class="mt-2 flex flex-wrap items-center gap-1.5">
          <span class="rounded px-1.5 py-0.5 text-xs {serviceStateColor(svcState, svcHealth)}">{statusDisplay}</span>
          {#each portsList() as port}
            <a href={port.url} target="_blank" rel="noreferrer">
              <span class="rounded bg-zinc-600 px-1.5 py-0.5 text-xs text-white hover:bg-zinc-500">{port.display}</span>
            </a>
          {/each}
        </div>
      {/if}
    </div>

    {#if !isEditMode && serviceCount > 1}
      <div class="flex shrink-0 gap-2">
        {#if isRunning}
          <button
            class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600"
            onclick={onrestart}
          >
            ↻ Restart
          </button>
          <button
            class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600"
            title="Recreate this container so saved compose changes take effect"
            onclick={onrecreate}
          >
            ♻ Recreate
          </button>
          <button
            class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600"
            onclick={onstop}
          >
            ⏹ Stop
          </button>
        {:else}
          <button
            class="rounded-sm bg-emerald-600 px-3 py-1.5 text-sm text-white hover:bg-emerald-500"
            onclick={onstart}
          >
            ▶ Start
          </button>
        {/if}
      </div>
    {/if}
  </div>

  {#if isEditMode}
    <div class="mt-3 flex gap-2">
      <button
        class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600"
        onclick={() => (showConfig = !showConfig)}
      >
        ✎ Edit
      </button>
      <button
        class="rounded-sm bg-red-700 px-3 py-1.5 text-sm text-white hover:bg-red-600"
        onclick={onremove}
      >
        🗑 Delete
      </button>
    </div>
  {/if}

  {#if isEditMode && showConfig}
    <div class="mt-4 space-y-4 border-t border-zinc-700 pt-4">
      <!-- Image -->
      <div>
        <label class="mb-1 block text-sm text-zinc-300" for="img-{name}">Docker Image</label>
        <input
          id="img-{name}"
          class="w-full rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
          bind:value={service.image}
        />
      </div>

      <!-- Ports -->
      <div>
        <span class="mb-1 block text-sm text-zinc-300">Ports</span>
        {#if networkMode === 'host'}
          <p class="text-xs text-amber-400">Port bindings are ignored with network_mode: host — the container shares the host's network directly.</p>
        {:else}
          <ArrayInput bind:values={service.ports} placeholder="HOST:CONTAINER" addLabel="Add port" />
        {/if}
      </div>

      <!-- Volumes -->
      <div>
        <span class="mb-1 block text-sm text-zinc-300">Volumes</span>
        <ArrayInput bind:values={service.volumes} placeholder="HOST:CONTAINER" addLabel="Add volume" />
      </div>

      <!-- Restart policy -->
      <div>
        <label class="mb-1 block text-sm text-zinc-300" for="restart-{name}">Restart Policy</label>
        <select
          id="restart-{name}"
          class="w-full rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
          bind:value={service.restart}
        >
          {#each RESTART_POLICIES as policy}
            <option value={policy}>{policy}</option>
          {/each}
        </select>
      </div>

      <!-- Environment -->
      <div>
        <span class="mb-1 block text-sm text-zinc-300">Environment Variables</span>
        <ArrayInput bind:values={service.environment} placeholder="KEY=VALUE" addLabel="Add variable" />
      </div>

      <!-- Network mode (issue #28) -->
      <div>
        <label class="mb-1 block text-sm text-zinc-300" for="netmode-{name}">Network Mode</label>
        <select
          id="netmode-{name}"
          class="w-full rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
          value={networkMode}
          onchange={(e) => setNetworkMode(e.currentTarget.value)}
        >
          <option value="">default (bridge)</option>
          <option value="host">host</option>
          <option value="none">none</option>
        </select>
        {#if hasCustomNetworkMode}
          <p class="mt-1 text-xs text-amber-400">
            Custom network_mode "{service.network_mode}" is set in YAML — choosing a mode here will replace it.
          </p>
        {/if}
      </div>

      <!-- Networks -->
      <div>
        <span class="mb-1 block text-sm text-zinc-300">Networks</span>
        {#if hasNetworkMode}
          <p class="text-xs text-amber-400">Disabled — network_mode: {networkMode} cannot be combined with network attachments.</p>
        {:else}
          <ArraySelect bind:values={service.networks} options={networkOptions} placeholder="Network name" addLabel="Add network" />
        {/if}
      </div>

      <!-- Depends on -->
      <div>
        <span class="mb-1 block text-sm text-zinc-300">Depends On</span>
        <ArrayInput bind:values={service.depends_on} placeholder="container name" addLabel="Add dependency" />
      </div>
    </div>
  {/if}
</div>
