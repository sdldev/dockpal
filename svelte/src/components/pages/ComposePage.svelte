<script lang="ts">
  // Compose page — faithful Svelte port of Dockge's Compose.vue.
  //
  // Two editing surfaces stay in sync:
  //   - YAML editors (CodeMirror) are the source of truth while focused;
  //   - the GUI forms mutate `jsonConfig`, which regenerates the YAML while
  //     preserving comments (jsonToYaml + copyYAMLComments).
  // The `editorFocus` flag is the one-way gate that prevents echo loops,
  // exactly like Dockge's implementation.
  import { onMount } from 'svelte';
  import type { Document } from 'yaml';
  // CodeMirror is ~500 KB and only used on this page; load it lazily so the
  // bundle stays off the critical path for every other route.
  type EditorModule = typeof import('../compose/CodeMirrorEditor.svelte');
  let CodeMirrorEditor = $state<EditorModule['default'] | undefined>(undefined);
  import ServiceCard from '../compose/ServiceCard.svelte';
  import NetworkEditor from '../compose/NetworkEditor.svelte';
  import ArrayInput from '../compose/ArrayInput.svelte';
  import Modal from '../ui/Modal.svelte';
  import {
    getStack, createStack, updateStackFiles, deleteStack, stackAction, deployStack,
    stackServiceAction, listDockerNetworks, watchDeploy, getGlobalEnv, setGlobalEnv,
    type Stack, type StackService
  } from '$lib/api/stacks';
  import { yamlToJson, jsonToYaml, envsubstYAML } from '$lib/yaml-sync';
  import { parseEnvFile } from '$lib/stack-utils';
  import { currentStackName, addToast, isOperator, selectedInstance } from '$lib/store';
  import { navigate } from '$lib/router';
  import { get } from 'svelte/store';

  // Instance this compose page targets (set on the Stacks page picker).
  const instanceId = get(selectedInstance) || 'local';

  const YAML_TEMPLATE = `
services:
  nginx:
    image: nginx:latest
    restart: unless-stopped
    ports:
      - "8080:80"
`;
  const ENV_DEFAULT = '# VARIABLE=value #comment';

  // --- stack state ---
  let stack = $state<Stack & { composeYAML: string; composeENV: string }>({
    name: '', status: 'unknown', statusText: '', managed: true,
    composeYAML: '', composeENV: ''
  });
  let isAdd = $state(false);
  let isEditMode = $state(false);
  let processing = $state(false);
  let showDeleteDialog = $state(false);

  // --- YAML ⇄ JSON two-way state ---
  let jsonConfig = $state<Record<string, any>>({ services: {} });
  let envsubstJSONConfig = $state<Record<string, any>>({ services: {} });
  let yamlDoc: Document | null = null;
  let editorFocus = $state(false);
  let yamlError = $state('');
  let yamlErrorTimeout: ReturnType<typeof setTimeout> | null = null;

  // --- runtime ---
  let serviceStatusMap = $state<Record<string, StackService>>({});
  let networkOptions = $state<string[]>([]);
  let newContainerName = $state('');

  // --- deploy log ---
  let deployLogs = $state<Array<{ time: string; message: string }>>([]);
  let showDeployLog = $state(false);
  let unwatchDeploy: (() => void) | null = null;

  // --- global env (shared by all stacks on this instance) ---
  let globalEnv = $state('');
  let showGlobalEnv = $state(false);
  let globalEnvDirty = $state(false);

  const active = $derived(stack.status === 'running');
  const serviceEntries = $derived(Object.entries(jsonConfig.services ?? {}));
  const networkList = $derived(Object.keys(jsonConfig.networks ?? {}));
  const stackUrls = $derived<string[]>(
    Array.isArray(envsubstJSONConfig?.['x-dockge']?.urls) ? envsubstJSONConfig['x-dockge'].urls : []
  );

  function urlDisplay(url: string): string {
    try {
      const obj = new URL(url);
      const pathname = obj.pathname === '/' ? '' : obj.pathname;
      return obj.host + pathname + obj.search;
    } catch {
      return url;
    }
  }

  // YAML → JSON (editor typing path)
  function yamlCodeChange() {
    try {
      const { config, doc } = yamlToJson(stack.composeYAML);
      yamlDoc = doc;
      jsonConfig = config;

      const env = parseEnvFile(stack.composeENV);
      const envYAML = envsubstYAML(stack.composeYAML, env);
      envsubstJSONConfig = yamlToJson(envYAML).config;

      if (yamlErrorTimeout) clearTimeout(yamlErrorTimeout);
      yamlError = '';
    } catch (e) {
      if (yamlErrorTimeout) clearTimeout(yamlErrorTimeout);
      const message = e instanceof Error ? e.message : String(e);
      if (yamlError) {
        yamlError = message;
      } else {
        yamlErrorTimeout = setTimeout(() => { yamlError = message; }, 3000);
      }
    }
  }

  // JSON → YAML (GUI form path) — regenerate with comments preserved.
  function regenerateYaml() {
    const { yaml, doc } = jsonToYaml(jsonConfig, yamlDoc);
    stack.composeYAML = yaml;
    yamlDoc = doc;
  }

  function onEditorChange(value: string) {
    stack.composeYAML = value;
    yamlCodeChange();
  }

  function onEnvChange(value: string) {
    stack.composeENV = value;
    yamlCodeChange();
  }

  function onFocusChange(focused: boolean) {
    editorFocus = focused;
  }

  // Any GUI-driven mutation of jsonConfig goes through here.
  function guiMutated() {
    if (editorFocus) return; // YAML is source of truth while typing
    regenerateYaml();
    // keep envsubst preview fresh too
    try {
      const env = parseEnvFile(stack.composeENV);
      envsubstJSONConfig = yamlToJson(envsubstYAML(stack.composeYAML, env)).config;
    } catch { /* keep last good preview */ }
  }

  async function loadStack(name: string) {
    processing = true;
    try {
      const s = await getStack(name, instanceId);
      stack = { ...s, composeYAML: s.composeYAML ?? '', composeENV: s.composeENV ?? '' };
      yamlCodeChange();
      refreshServiceStatus(s);
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Failed to load stack', 'error');
    } finally {
      processing = false;
    }
  }

  function refreshServiceStatus(s: Stack) {
    const map: Record<string, StackService> = {};
    for (const svc of s.services ?? []) map[svc.name] = svc;
    serviceStatusMap = map;
  }

  async function refreshNetworks() {
    try {
      const res = await listDockerNetworks(instanceId);
      networkOptions = res.networks ?? [];
    } catch {
      networkOptions = [];
    }
  }

  async function loadGlobalEnv() {
    try {
      const res = await getGlobalEnv(instanceId);
      globalEnv = res.content ?? '';
      globalEnvDirty = false;
    } catch {
      globalEnv = '';
    }
  }

  async function saveGlobalEnv() {
    try {
      await setGlobalEnv(globalEnv, instanceId);
      globalEnvDirty = false;
      addToast('Global env saved', 'success');
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Failed to save global env', 'error');
    }
  }

  onMount(() => {
    // Fire-and-forget: the editor renders once the chunk resolves (guarded below).
    void import('../compose/CodeMirrorEditor.svelte').then((m) => {
      CodeMirrorEditor = m.default;
    });
    const name = get(currentStackName);
    if (!name) {
      // Add mode
      isAdd = true;
      isEditMode = true;
      processing = false;
      stack = {
        name: '', status: 'unknown', statusText: '', managed: true,
        composeYAML: YAML_TEMPLATE, composeENV: ENV_DEFAULT
      };
      yamlCodeChange();
    } else {
      loadStack(name);
    }
    refreshNetworks();
    loadGlobalEnv();
    return () => {
      unwatchDeploy?.();
      if (yamlErrorTimeout) clearTimeout(yamlErrorTimeout);
    };
  });

  // --- actions ---

  function ensureServicesValid(): boolean {
    if (!jsonConfig.services || typeof jsonConfig.services !== 'object' || Array.isArray(jsonConfig.services)) {
      addToast('Services must be an object', 'error');
      return false;
    }
    if (Object.keys(jsonConfig.services).length === 0) {
      addToast('No services found in compose.yaml', 'error');
      return false;
    }
    return true;
  }

  function fillDefaultStackName() {
    if (stack.name) return;
    const names = Object.keys(jsonConfig.services ?? {});
    if (names.length > 0) {
      const first = jsonConfig.services[names[0]];
      stack.name = (first?.container_name ?? names[0]).toLowerCase();
    }
  }

  function startDeployWatch(deployId: string) {
    deployLogs = [];
    showDeployLog = true;
    unwatchDeploy?.();
    unwatchDeploy = watchDeploy(deployId, (msg) => {
      const text = msg.message ?? '';
      if (!text) return;
      deployLogs = [...deployLogs, { time: msg.time ?? '', message: text }];
    }, () => {
      loadStack(stack.name); // refresh status when stream ends
    }, instanceId);
  }

  async function doDeploy() {
    if (!ensureServicesValid()) return;
    fillDefaultStackName();
    processing = true;
    try {
      const res = await deployStack(stack.name, stack.composeYAML, stack.composeENV, isAdd, instanceId);
      addToast('Deploy started', 'info');
      isEditMode = false;
      isAdd = false;
      currentStackName.set(stack.name);
      startDeployWatch(res.deploy_id);
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Deploy failed', 'error');
    } finally {
      processing = false;
    }
  }

  async function doSave() {
    processing = true;
    try {
      if (isAdd) {
        fillDefaultStackName();
        await createStack(stack.name, stack.composeYAML, stack.composeENV, instanceId);
        isAdd = false;
        currentStackName.set(stack.name);
        addToast('Saved', 'success');
      } else {
        await updateStackFiles(stack.name, stack.composeYAML, stack.composeENV, instanceId);
        addToast('Saved', 'success');
      }
      isEditMode = false;
      // Refetch with runtime status (draft/running/...) + services.
      const full = await getStack(stack.name, instanceId);
      stack = { ...full, composeYAML: stack.composeYAML, composeENV: stack.composeENV };
      refreshServiceStatus(full);
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Save failed', 'error');
    } finally {
      processing = false;
    }
  }

  async function doAction(action: 'start' | 'stop' | 'restart' | 'update' | 'down') {
    processing = true;
    try {
      const s = await stackAction(stack.name, action, instanceId);
      stack = { ...stack, status: s.status, statusText: s.statusText };
      refreshServiceStatus(s);
      addToast(`${action} ok`, 'success');
    } catch (e) {
      addToast(e instanceof Error ? e.message : `${action} failed`, 'error');
    } finally {
      processing = false;
    }
  }

  async function doDelete() {
    showDeleteDialog = false;
    processing = true;
    try {
      await deleteStack(stack.name, instanceId);
      addToast('Stack deleted', 'success');
      currentStackName.set(null);
      navigate('stacks');
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
    } finally {
      processing = false;
    }
  }

  function discardChanges() {
    if (stack.name) loadStack(stack.name);
    isEditMode = false;
  }

  function addContainer() {
    const name = newContainerName.trim();
    if (!name) {
      addToast('Container name cannot be empty', 'error');
      return;
    }
    if (jsonConfig.services[name]) {
      addToast('Container name already exists', 'error');
      return;
    }
    // Placeholder image so the project stays deployable; user edits via form.
    jsonConfig.services[name] = { image: 'busybox:latest', restart: 'unless-stopped' };
    newContainerName = '';
    guiMutated();
  }

  function removeService(name: string) {
    delete jsonConfig.services[name];
    guiMutated();
  }

  async function serviceAction(name: string, action: 'up' | 'stop' | 'restart') {
    processing = true;
    try {
      const s = await stackServiceAction(stack.name, name, action, instanceId);
      refreshServiceStatus(s);
      addToast(`${action} ${name} ok`, 'success');
    } catch (e) {
      addToast(e instanceof Error ? e.message : `${action} ${name} failed`, 'error');
    } finally {
      processing = false;
    }
  }

  // x-dockge URLs binding helper (kept outside services).
  function getXdockgeUrls(): string[] {
    return (jsonConfig['x-dockge']?.urls ?? []) as string[];
  }
  function setXdockgeUrls(v: string[]) {
    if (!jsonConfig['x-dockge']) jsonConfig['x-dockge'] = {};
    jsonConfig['x-dockge'].urls = v;
  }

  // JSON deep-mutation bridge: ServiceCard mutates its bound `service` object;
  // we detect those edits via $effect + JSON snapshot comparison and regen.
  let lastJsonSnapshot = '';
  $effect(() => {
    const snapshot = JSON.stringify(jsonConfig);
    if (lastJsonSnapshot && snapshot !== lastJsonSnapshot && !editorFocus) {
      regenerateYaml();
    }
    lastJsonSnapshot = snapshot;
  });
</script>

<div class="max-w-7xl">
  <!-- Header -->
  {#if isAdd}
    <h1 class="mb-4 text-2xl font-bold text-white">Compose</h1>
  {:else}
    <div class="mb-4 flex items-center gap-3">
      <span class="rounded px-2 py-0.5 text-xs font-medium
        {stack.status === 'running' ? 'bg-emerald-600 text-white' : stack.status === 'exited' ? 'bg-red-600 text-white' : 'bg-zinc-600 text-white'}">
        {stack.status}
      </span>
      <h1 class="text-2xl font-bold text-white">{stack.name}</h1>
    </div>
  {/if}

  <!-- Toolbar -->
  {#if $isOperator}
    <div class="mb-4 flex flex-wrap items-center gap-2">
      {#if isEditMode}
        <button
          class="rounded-sm bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-500 disabled:opacity-50"
          disabled={processing}
          onclick={doDeploy}
        >
          🚀 Deploy
        </button>
        <button
          class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
          disabled={processing}
          onclick={doSave}
        >
          💾 Save
        </button>
        {#if !isAdd}
          <button
            class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
            disabled={processing}
            onclick={discardChanges}
          >
            Discard
          </button>
        {/if}
      {:else}
        <button
          class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
          disabled={processing}
          onclick={() => (isEditMode = true)}
        >
          ✎ Edit
        </button>
        {#if !active}
          <button
            class="rounded-sm bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-500 disabled:opacity-50"
            disabled={processing}
            onclick={() => doAction('start')}
          >
            ▶ Start
          </button>
        {:else}
          <button
            class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
            disabled={processing}
            onclick={() => doAction('restart')}
          >
            ↻ Restart
          </button>
        {/if}
        <button
          class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
          disabled={processing}
          onclick={() => doAction('update')}
        >
          ⬇ Update
        </button>
        {#if active}
          <button
            class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
            disabled={processing}
            onclick={() => doAction('stop')}
          >
            ⏹ Stop
          </button>
        {/if}
        <button
          class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
          disabled={processing}
          onclick={() => doAction('down')}
        >
          ⏏ Down
        </button>
        <button
          class="rounded-sm bg-red-700 px-3 py-1.5 text-sm text-white hover:bg-red-600 disabled:opacity-50"
          disabled={processing}
          onclick={() => (showDeleteDialog = true)}
        >
          🗑 Delete
        </button>
      {/if}
      <button
        class="ml-auto rounded-sm px-3 py-1.5 text-sm text-zinc-400 hover:text-zinc-200"
        onclick={() => navigate('stacks')}
      >
        ← Back to stacks
      </button>
    </div>
  {/if}

  <!-- URLs -->
  {#if stackUrls.length > 0}
    <div class="mb-4 flex flex-wrap gap-2">
      {#each stackUrls as url}
        <a href={url} target="_blank" rel="noreferrer">
          <span class="rounded bg-zinc-600 px-2 py-0.5 text-xs text-white hover:bg-zinc-500">{urlDisplay(url)}</span>
        </a>
      {/each}
    </div>
  {/if}

  <!-- Deploy log -->
  {#if showDeployLog}
    <div class="mb-4 rounded-md border border-zinc-700 bg-black p-3 font-mono text-xs text-zinc-200">
      <div class="mb-1 flex items-center justify-between">
        <span class="text-zinc-400">Deploy log</span>
        <button class="text-zinc-500 hover:text-zinc-300" onclick={() => (showDeployLog = false)}>✕</button>
      </div>
      <div class="max-h-48 overflow-auto whitespace-pre-wrap">
        {#each deployLogs as line}
          <div><span class="text-zinc-500">{line.time}</span> {line.message}</div>
        {/each}
      </div>
    </div>
  {/if}

  <div class="grid gap-6 lg:grid-cols-2">
    <!-- Left column -->
    <div>
      {#if isAdd}
        <h4 class="mb-3 text-lg font-semibold text-white">General</h4>
        <div class="mb-4 rounded-md border border-zinc-700 bg-zinc-800/60 p-4">
          <label class="mb-1 block text-sm text-zinc-300" for="stack-name">Stack Name</label>
          <input
            id="stack-name"
            class="w-full rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
            bind:value={stack.name}
            onblur={() => (stack.name = stack.name.toLowerCase())}
          />
          <p class="mt-1 text-xs text-zinc-500">Lowercase only</p>
        </div>
      {/if}

      <h4 class="mb-3 text-lg font-semibold text-white">Containers</h4>

      {#if isEditMode && $isOperator}
        <div class="mb-3 flex gap-2">
          <input
            class="flex-1 rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
            placeholder="New Container Name…"
            bind:value={newContainerName}
            onkeydown={(e) => e.key === 'Enter' && addContainer()}
          />
          <button
            class="rounded-sm bg-emerald-600 px-3 py-1 text-sm text-white hover:bg-emerald-500"
            onclick={addContainer}
          >
            Add Container
          </button>
        </div>
      {/if}

      <div class="space-y-3">
        {#each serviceEntries as [name] (name)}
          <ServiceCard
            {name}
            bind:service={jsonConfig.services[name]}
            envsubstService={envsubstJSONConfig.services?.[name] ?? {}}
            {isEditMode}
            serviceStatus={serviceStatusMap[name] ?? null}
            serviceCount={serviceEntries.length}
            networkOptions={networkList}
            onstart={() => serviceAction(name, 'up')}
            onstop={() => serviceAction(name, 'stop')}
            onrestart={() => serviceAction(name, 'restart')}
            onremove={() => removeService(name)}
          />
        {/each}
      </div>

      {#if isEditMode && $isOperator}
        <h4 class="mb-3 mt-6 text-lg font-semibold text-white">Extra</h4>
        <div class="mb-4 rounded-md border border-zinc-700 bg-zinc-800/60 p-4">
          <span class="mb-1 block text-sm text-zinc-300">URLs</span>
          <ArrayInput
            values={getXdockgeUrls()}
            placeholder="https://"
            addLabel="Add URL"
            onchange={setXdockgeUrls}
          />
        </div>
      {/if}
    </div>

    <!-- Right column -->
    <div>
      <h4 class="mb-3 text-lg font-semibold text-white">compose.yaml</h4>
      <div class="mb-2">
        {#if CodeMirrorEditor}
          <CodeMirrorEditor
            value={stack.composeYAML}
            disabled={!isEditMode}
            onchange={onEditorChange}
            onfocuschange={onFocusChange}
          />
        {:else}
          <div class="codemirror-host flex min-h-48 items-center justify-center rounded-md border border-zinc-700 bg-[#282a36] text-sm text-zinc-500">Loading editor…</div>
        {/if}
      </div>
      {#if isEditMode && yamlError}
        <div class="mb-3 rounded-sm border border-red-800 bg-red-900/30 px-3 py-2 text-sm text-red-300">
          {yamlError}
        </div>
      {/if}

      {#if isEditMode}
        <h4 class="mb-3 text-lg font-semibold text-white">.env</h4>
        <div class="mb-4">
          {#if CodeMirrorEditor}
            <CodeMirrorEditor
              value={stack.composeENV}
              disabled={!isEditMode}
              onchange={onEnvChange}
              onfocuschange={onFocusChange}
            />
          {:else}
            <div class="codemirror-host flex min-h-48 items-center justify-center rounded-md border border-zinc-700 bg-[#282a36] text-sm text-zinc-500">Loading editor…</div>
          {/if}
        </div>

        <h4 class="mb-3 text-lg font-semibold text-white">Networks</h4>
        <div class="mb-4 rounded-md border border-zinc-700 bg-zinc-800/60 p-4">
          <NetworkEditor
            bind:networks={jsonConfig.networks}
            externalOptions={networkOptions.filter((n) => !n.startsWith(stack.name + '_'))}
          />
        </div>

        {#if $isOperator}
          <h4 class="mb-3 text-lg font-semibold text-white">
            <button
              class="flex items-center gap-2 text-zinc-300 hover:text-white"
              onclick={() => (showGlobalEnv = !showGlobalEnv)}
            >
              <span>{showGlobalEnv ? '▾' : '▸'}</span>
              Global Env
              <span class="text-xs font-normal text-zinc-500">(shared by all stacks on this instance)</span>
            </button>
          </h4>
          {#if showGlobalEnv}
            <div class="mb-4">
              <div class="mb-2">
                {#if CodeMirrorEditor}
                  <CodeMirrorEditor
                    value={globalEnv}
                    placeholder="# KEY=value"
                    onchange={(v) => { globalEnv = v; globalEnvDirty = true; }}
                  />
                {:else}
                  <div class="codemirror-host flex min-h-48 items-center justify-center rounded-md border border-zinc-700 bg-[#282a36] text-sm text-zinc-500">Loading editor…</div>
                {/if}
              </div>
              <button
                class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
                disabled={!globalEnvDirty || processing}
                onclick={saveGlobalEnv}
              >
                💾 Save Global Env
              </button>
            </div>
          {/if}
        {/if}
      {/if}
    </div>
  </div>

  <!-- Delete confirmation -->
  <Modal
    open={showDeleteDialog}
    title="Delete stack"
    onclose={() => (showDeleteDialog = false)}
  >
    <p class="text-sm text-zinc-300">
      Delete stack <span class="font-semibold text-white">{stack.name}</span>?
      This runs <code class="text-zinc-400">docker compose down --remove-orphans</code> and removes the stack directory.
    </p>
    <div class="mt-4 flex justify-end gap-2">
      <button
        class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600"
        onclick={() => (showDeleteDialog = false)}
      >
        Cancel
      </button>
      <button
        class="rounded-sm bg-red-700 px-3 py-1.5 text-sm text-white hover:bg-red-600"
        onclick={doDelete}
      >
        Delete
      </button>
    </div>
  </Modal>
</div>
