<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '$lib/api/client';
  import { templates, selectedInstance, addToast } from '$lib/store';
  import type { Template } from '$lib/types/api';
  import Modal from '../ui/Modal.svelte';
  import DeployWizard from '../deploy/DeployWizard.svelte';
  import Button from '../ui/Button.svelte';

  let loading = $state(true);
  let deployTemplate = $state<Template | null>(null);
  let deployCustom = $state(false);

  // Batch C: add compose/git deployment tabs
  const tabs = ['templates', 'compose', 'git'] as const;
  type Tab = (typeof tabs)[number];
  let activeTab = $state<Tab>('templates');

  // Catalog browsing
  let search = $state('');
  let category = $state('all');

  // Compose mode form
  let showComposeForm = $state(false);
  let composeName = $state('');
  let composeDomain = $state('');
  let composeYaml = $state('');
  let composing = $state(false);

  // Git mode form
  let showGitForm = $state(false);
  let gitRepo = $state('');
  let gitBranch = $state('main');
  let gitPath = $state('.');
  let deployingGit = $state(false);

  onMount(async () => {
    try {
      const list = await api.get<Template[]>('/templates');
      templates.set(list);
    } catch {
      addToast('Failed to load templates', 'error');
    } finally {
      loading = false;
    }
  });

  const categories = $derived(
    ['all', ...new Set(($templates ?? []).map((t) => t.category).filter(Boolean))].sort()
  );

  const filteredTemplates = $derived.by(() => {
    const list = $templates ?? [];
    const q = search.trim().toLowerCase();
    return list.filter((t) => {
      if (category !== 'all' && t.category !== category) return false;
      if (!q) return true;
      const haystack = [t.name, t.description, t.category, ...(t.tags ?? [])].join(' ').toLowerCase();
      return haystack.includes(q);
    });
  });

  const popularCount = $derived(($templates ?? []).filter((t) => t.popular).length);

  function startDeploy(tpl: Template) {
    deployTemplate = tpl;
    deployCustom = false;
  }

  function startCustomInstall() {
    deployTemplate = null;
    deployCustom = true;
  }

  async function submitCompose() {
    if (!composeName.trim()) {
      addToast('Compose name required', 'error');
      return;
    }
    if (!composeYaml.trim()) {
      addToast('YAML required', 'error');
      return;
    }
    composing = true;
    try {
      await api.post('/deploy/compose', {
        name: composeName.trim(),
        domain: composeDomain.trim() || undefined,
        compose: composeYaml.trim()
      });
      addToast('Compose deployed', 'success');
      showComposeForm = false;
      composeName = '';
      composeYaml = '';
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Deploy failed', 'error');
    } finally {
      composing = false;
    }
  }

  async function triggerGitDeploy() {
    if (!gitRepo.trim()) {
      addToast('Repo required', 'error');
      return;
    }
    deployingGit = true;
    try {
      await api.post('/deploy/git', {
        repo: gitRepo.trim(),
        branch: gitBranch.trim(),
        path: gitPath.trim()
      });
      addToast('Git deployment triggered', 'success');
      showGitForm = false;
      gitRepo = '';
      gitBranch = 'main';
      gitPath = '.';
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Deploy failed', 'error');
    } finally {
      deployingGit = false;
    }
  }
</script>

<div class="space-y-4">
  <div class="flex items-center justify-between">
    <div>
      <h2 class="text-lg font-semibold text-white">App Installer</h2>
      <p class="text-sm text-zinc-500">Deploy from the app catalog, a custom image, Compose YAML, or Git repo</p>
    </div>
  </div>

  <div class="flex gap-1 border-b border-zinc-800">
    {#each tabs as tab}
      <button
        onclick={() => { activeTab = tab; }}
        class="px-3 py-2 text-sm transition-colors border-b-2 -mb-px"
        class:border-white={activeTab === tab}
        class:text-white={activeTab === tab}
        class:border-transparent={activeTab !== tab}
        class:text-zinc-500={activeTab !== tab}
        class:hover:text-zinc-300={activeTab !== tab}
      >
        {tab.charAt(0).toUpperCase() + tab.slice(1)}
      </button>
    {/each}
  </div>

  {#if activeTab === 'templates'}
    <!-- Hero -->
    <div class="bg-gradient-to-r from-blue-600/10 via-indigo-600/10 to-purple-600/10 border border-zinc-800 rounded-sm p-5 flex items-center justify-between gap-4">
      <div>
        <p class="text-sm font-semibold text-white">One-click app catalog</p>
        <p class="text-xs text-zinc-400 mt-1">
          {($templates ?? []).length} templates · {popularCount} popular · or install any Docker image manually
        </p>
      </div>
      <Button variant="primary" onclick={startCustomInstall}>Custom Install</Button>
    </div>

    <!-- Toolbar -->
    <div class="flex flex-col sm:flex-row gap-3">
      <input
        type="text"
        bind:value={search}
        placeholder="Search apps, tags, or categories..."
        class="flex-1 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white placeholder:text-zinc-600"
      />
      <div class="flex gap-1 overflow-x-auto pb-1 sm:pb-0">
        {#each categories as cat}
          <button
            onclick={() => { category = cat; }}
            class="px-3 py-1.5 text-xs rounded-sm border whitespace-nowrap transition-colors"
            class:bg-white={category === cat}
            class:text-zinc-900={category === cat}
            class:border-zinc-700={category === cat}
            class:bg-zinc-900={category !== cat}
            class:text-zinc-400={category !== cat}
            class:border-zinc-800={category !== cat}
            class:hover:text-white={category !== cat}
          >
            {cat === 'all' ? 'All' : cat}
          </button>
        {/each}
      </div>
    </div>

    <!-- Grid -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
      {#each filteredTemplates as tpl (tpl.id)}
        <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5 hover:border-zinc-700 transition-colors flex flex-col">
          <div class="flex items-start gap-3 mb-2">
            <div class="w-12 h-12 rounded-sm bg-zinc-950 border border-zinc-800 flex items-center justify-center shrink-0 overflow-hidden">
              {#if tpl.icon_url}
                <img src={tpl.icon_url} alt={tpl.name} class="w-7 h-7 object-contain" loading="lazy" />
              {:else}
                <span class="text-2xl">{tpl.icon}</span>
              {/if}
            </div>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <h3 class="text-sm font-semibold text-white truncate">{tpl.name}</h3>
                {#if tpl.popular}
                  <span class="px-1.5 py-0.5 rounded text-[10px] font-medium bg-amber-400/10 text-amber-400 border border-amber-400/20 shrink-0">Popular</span>
                {/if}
              </div>
              <span class="text-xs text-zinc-500 capitalize">{tpl.category}</span>
            </div>
          </div>
          <p class="text-xs text-zinc-400 mb-3 line-clamp-2 flex-1">{tpl.description}</p>
          {#if tpl.tags && tpl.tags.length > 0}
            <div class="flex flex-wrap gap-1 mb-3">
              {#each tpl.tags.slice(0, 4) as tag}
                <span class="px-1.5 py-0.5 rounded text-[10px] bg-zinc-950 border border-zinc-800 text-zinc-500">{tag}</span>
              {/each}
              {#if (tpl.tags ?? []).length > 4}
                <span class="px-1.5 py-0.5 rounded text-[10px] bg-zinc-950 border border-zinc-800 text-zinc-600">+{(tpl.tags ?? []).length - 4}</span>
              {/if}
            </div>
          {/if}
          <button
            onclick={() => startDeploy(tpl)}
            class="w-full py-2 bg-white hover:bg-zinc-200 text-zinc-900 rounded-sm text-xs font-medium transition-colors"
          >
            Install
          </button>
        </div>
      {:else}
        <div class="col-span-full text-center py-12 text-zinc-600 text-sm">
          {loading ? 'Loading templates...' : filteredTemplates.length === 0 && search ? 'No templates match your search' : 'No templates available'}
        </div>
      {/each}
    </div>
  {:else if activeTab === 'compose'}
    <div class="space-y-4">
      <form class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-3" onsubmit={(e) => { e.preventDefault(); submitCompose(); }}>
        <div>
          <label for="compose-name" class="block text-xs font-medium text-zinc-400 mb-1">Project name</label>
          <input id="compose-name" type="text" bind:value={composeName} placeholder="my-app"
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
        </div>
        <div>
          <label for="compose-domain" class="block text-xs font-medium text-zinc-400 mb-1">Domain (optional)</label>
          <input id="compose-domain" type="text" bind:value={composeDomain} placeholder="app.example.com"
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
        </div>
        <div>
          <label for="compose-yaml" class="block text-xs font-medium text-zinc-400 mb-1">Docker Compose YAML</label>
          <textarea
            bind:value={composeYaml}
            placeholder="services:\n  web:\n    image: nginx:latest\n    ports:\n      - '80:80'"
            rows="12"
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
          ></textarea>
        </div>
        <div class="flex justify-end gap-2">
          <Button variant="secondary" onclick={() => { showComposeForm = !showComposeForm; }}>Cancel</Button>
          <Button type="submit" loading={composing}>Deploy Compose</Button>
        </div>
      </form>
    </div>
  {:else if activeTab === 'git'}
    <div class="space-y-4">
      <form class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-3" onsubmit={(e) => { e.preventDefault(); triggerGitDeploy(); }}>
        <div>
          <label for="git-repo" class="block text-xs font-medium text-zinc-400 mb-1">Git repository</label>
          <input id="git-repo" type="text" bind:value={gitRepo} placeholder="owner/repo"
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label for="git-branch" class="block text-xs font-medium text-zinc-400 mb-1">Branch</label>
            <input id="git-branch" type="text" bind:value={gitBranch} placeholder="main"
              class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
          </div>
          <div>
            <label for="git-path" class="block text-xs font-medium text-zinc-400 mb-1">Path in repo</label>
            <input id="git-path" type="text" bind:value={gitPath} placeholder="."
              class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
          </div>
        </div>
        <div class="flex justify-end gap-2">
          <Button variant="secondary" onclick={() => { showGitForm = !showGitForm; }}>Cancel</Button>
          <Button type="submit" loading={deployingGit}>Deploy Git</Button>
        </div>
      </form>
    </div>
  {/if}
</div>

<Modal
  open={deployTemplate !== null || deployCustom}
  title={deployCustom ? 'Custom App Install' : deployTemplate ? `Install ${deployTemplate.name}` : ''}
  size="lg"
  onclose={() => { deployTemplate = null; deployCustom = false; }}
>
  {#if deployTemplate !== null || deployCustom}
    <DeployWizard
      template={deployTemplate}
      custom={deployCustom}
      instanceId={$selectedInstance}
      ondone={() => { deployTemplate = null; deployCustom = false; }}
    />
  {/if}
</Modal>