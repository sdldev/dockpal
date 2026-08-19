# Phase 2: Component Migration - Core UI Elements

## Overview
Migrate existing Alpine.js components to Svelte components. Prioritize reusing common patterns from vanilla implementation.

## Timeline
**Duration:** 5-7 days
**Priority:** High

---

## Task List

### 2.1 Common Components Library

#### Create `src/components/ui/Button.svelte`

```svelte
<script lang="ts">
  interface Props {
    variant?: 'primary' | 'secondary' | 'danger';
    size?: 'sm' | 'md' | 'lg';
    disabled?: boolean;
    loading?: boolean;
    type?: 'button' | 'submit' | 'reset';
  }

  let {
    variant = 'primary',
    size = 'md',
    disabled = false,
    loading = false,
    type = 'button',
    classes = ''
  }: Props = $props();

  const buttonClasses = computed(() => {
    return [
      'font-medium rounded-sm transition-colors active:scale-[0.98]',
      variant === 'primary' && 'bg-white text-zinc-900 hover:bg-zinc-200',
      variant === 'secondary' && 'bg-zinc-800 text-white hover:bg-zinc-700 border border-zinc-700',
      variant === 'danger' && 'bg-red-600 text-white hover:bg-red-700',
      size === 'sm' && 'px-3 py-1.5 text-xs',
      size === 'md' && 'px-4 py-2 text-sm',
      size === 'lg' && 'px-6 py-3 text-base',
      disabled || loading ? 'opacity-50 cursor-not-allowed' : '',
      classes
    ].filter(Boolean).join(' ');
  });
</script>

<button 
  type={type} 
  class={buttonClasses}
  disabled={disabled || loading}>
  {#if loading}
    <svg class="animate-spin h-4 w-4" viewBox="0 0 24 24">
      <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" fill="none"/>
      <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/>
    </svg>
    <slot />
  {:else}
    <slot />
  {/if}
</button>
```

#### Create `src/lib/store.ts`

```typescript
import { writable } from 'svelte/store';

// User authentication store
export const user = writable<User | null>(null);

// Current deploy session
export const deploySession = writable<{
  id: string;
  name: string;
  status: 'pending' | 'running' | 'done' | 'error';
  logs: DeployLog[];
} | null>(null);

// Template configuration form
export const templateConfig = writable<TemplateConfig>({
  template: null,
  name: '',
  env: {},
  ports: {},
  restartPolicy: 'unless-stopped',
  networkMode: 'bridge',
  customNetwork: '',
  autoRecover: false,
  domain: '',
  deploying: false,
  error: ''
});

// Services list (cached)
export const services = writable<Service[]>([]);

// Navigation state
export const currentPage = writable<string>('dashboard');
```

#### Create `src/lib/store.ts` continued

```typescript
interface TemplateConfig {
  template: Template | null;
  name: string;
  env: Record<string, string>;
  envText: string;
  ports: Record<number, number>;
  restartPolicy: string;
  networkMode: string;
  customNetwork: string;
  autoRecover: boolean;
  domain: string;
  deploying: boolean;
  error: string;
  activeTab: 'environment' | 'ports' | 'network' | 'advanced' | 'logs';
  logs: Array<{ time: string; step: string; message: string; status?: string }>;
}

export const createTemplateStore = () => {
  const { subscribe, update, set } = writable<TemplateConfig>({
    template: null,
    name: '',
    env: {},
    envText: '',
    ports: {},
    restartPolicy: 'unless-stopped',
    networkMode: 'bridge',
    customNetwork: '',
    autoRecover: false,
    domain: '',
    deploying: false,
    error: '',
    activeTab: 'environment',
    logs: []
  });

  return { subscribe, update, set };
};

export const templateStore = createTemplateStore();
```

---

### 2.2 Service Cards & Lists

#### Migrate Dashboard Card Component

**Original (Alpine.js):**
```html
<div x-data="{ cards: [...] }" class="grid grid-cols-2 lg:grid-cols-3 gap-3 mb-6">
  <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
    <div class="flex items-center justify-between mb-2">
      <span class="text-2xl">📊</span>
      <span class="text-xs text-zinc-400">Containers</span>
    </div>
    <div class="text-2xl font-bold" x-text="cards.container.total"></div>
  </div>
</div>
```

**New (Svelte):**

```svelte
<!-- src/components/Dashboard/Card.svelte -->
<script lang="ts">
  interface Props {
    icon: string;
    label: string;
    value: number | string;
    trend?: 'up' | 'down' | 'stable';
    trendValue?: string;
  }

  let { icon, label, value, trend, trendValue }: Props = $props();

  const trendColor = computed(() => {
    switch(trend) {
      case 'up': return 'text-emerald-400';
      case 'down': return 'text-red-400';
      default: return 'text-zinc-400';
    }
  });
</script>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
  <div class="flex items-center justify-between mb-2">
    <span class="text-2xl">{icon}</span>
    <span class="text-xs text-zinc-400">{label}</span>
  </div>
  <div class="text-2xl font-bold text-white">
    {value}
  </div>
  {#if trend && trendValue}
    <div class="mt-1 text-xs flex items-center gap-1">
      <span class={trendColor}>{trendValue}</span>
    </div>
  {/if}
</div>
```

#### Create Service Table Component

```svelte
<!-- src/components/Services/Table.svelte -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '$lib/api/client';
  import { services } from '$lib/store';
  
  let selectedService: Service | null = null;
  let loading = true;

  async function loadServices() {
    try {
      const data = await api.get<Service[]>('/api/services');
      services.set(data);
    } catch (error) {
      console.error('Failed to load services:', error);
    } finally {
      loading = false;
    }
  }

  function getStatusColor(status: string): string {
    switch(status) {
      case 'running': return 'text-emerald-400 bg-emerald-400/10';
      case 'stopped': return 'text-zinc-400 bg-zinc-400/10';
      case 'degraded': return 'text-amber-400 bg-amber-400/10';
      case 'error': return 'text-red-400 bg-red-400/10';
      default: return 'text-zinc-400 bg-zinc-400/10';
    }
  }

  onMount(loadServices);
</script>

{#if loading}
  <div class="text-center py-12 text-zinc-500">
    <svg class="animate-spin h-6 w-6 mx-auto mb-2" viewBox="0 0 24 24">
      <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" fill="none"/>
      <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/>
    </svg>
    Loading services...
  </div>
{:else}
  <table class="w-full">
    <thead>
      <tr class="border-b border-zinc-800">
        <th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Name</th>
        <th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Status</th>
        <th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500 hidden sm:table-cell">Type</th>
        <th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500 hidden md:table-cell">Ports</th>
        <th class="text-right px-4 py-2.5 text-xs font-medium text-zinc-500">Actions</th>
      </tr>
    </thead>
    <tbody>
      {#each $services as service (service.id)}
        <tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
          <td class="px-4 py-2.5 text-sm text-white">{service.name}</td>
          <td class="px-4 py-2.5">
            <span class="px-2 py-1 rounded text-xs font-medium {getStatusColor(service.status)}">
              {service.status}
            </span>
          </td>
          <td class="px-4 py-2.5 text-sm text-zinc-400 hidden sm:table-cell">{service.type}</td>
          <td class="px-4 py-2.5 text-sm text-zinc-400 hidden md:table-cell">
            {#each service.ports.slice(0, 2) as port}
              <span class="inline-block mr-2 font-mono text-xs">
                {port.host_port}:{port.container_port}
              </span>
            {/each}
          </td>
          <td class="px-4 py-2.5 text-right space-x-2">
            <button class="text-blue-400 hover:text-blue-300" onclick="window.location='/container/{service.id}'">
              View
            </button>
            <button class="text-zinc-400 hover:text-white" title="Delete">
              Delete
            </button>
          </td>
        </tr>
      {/each}
      
      {#if $services.length === 0}
        <tr>
          <td colspan="5" class="text-center py-8 text-zinc-600">No services found</td>
        </tr>
      {/if}
    </tbody>
  </table>
{/if}
```

---

### 2.3 Deployment Wizard

#### Create multi-step deployment form component

```svelte
<!-- src/components/Deploy/Wizard.svelte -->
<script lang="ts">
  import { onMount } from 'svelte';
  import Button from '../ui/Button.svelte';
  import { templateConfig } from '$lib/store';
  import { api } from '$lib/api/client';

  export let templateId: string;

  let currentStep = 0;
  let steps = ['environment', 'ports', 'network', 'advanced'];
  let deploying = false;

  async function deploy() {
    deploying = true;
    
    try {
      const payload = {
        ...$templateConfig,
        ports: Object.entries($templateConfig.ports).map(([k, v]) => ({ key: k, value: v }))
      };
      
      await api.post(`/api/templates/${templateId}/deploy/stream`, payload);
      // Stream updates via WebSocket
    } catch (error) {
      $templateConfig.update(tc => ({ ...tc, error: error.message }));
    } finally {
      deploying = false;
    }
  }

  function nextStep() {
    if (currentStep < steps.length - 1) {
      currentStep++;
    }
  }

  function prevStep() {
    if (currentStep > 0) {
      currentStep--;
    }
  }
</script>

<div class="space-y-6">
  <!-- Progress indicator -->
  <div class="flex items-center gap-2">
    {#each steps as step, idx}
      <div class="flex items-center">
        <div class="w-8 h-8 rounded-full flex items-center justify-center text-xs font-medium bg-{idx <= currentStep ? 'blue-600' : 'zinc-700'} text-white">
          {idx + 1}
        </div>
        {#if idx < steps.length - 1}
          <div class="w-16 h-1 bg-zinc-700 ml-2"></div>
        {/if}
      </div>
    {/each}
  </div>

  <!-- Step content -->
  <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-6">
    {#if steps[currentStep] === 'environment'}
      <h3 class="text-lg font-semibold text-white mb-4">Environment Variables</h3>
      <div class="space-y-3">
        {#each $templateConfig.template.env_required as key}
          <div>
            <label class="block text-xs font-medium text-zinc-400 mb-1 font-mono">{key}</label>
            <input 
              type="{key.toLowerCase().includes('password') ? 'password' : 'text'}"
              bind:value={$templateConfig.env[key]}
              class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
              placeholder="Enter value..."
            />
          </div>
        {/each}
        
        {#if !$templateConfig.template?.env_required?.length}
          <p class="text-sm text-zinc-500 italic">No environment variables required.</p>
        {/if}
      </div>
    {:else if steps[currentStep] === 'ports'}
      <h3 class="text-lg font-semibold text-white mb-4">Port Mapping</h3>
      <div class="space-y-3">
        {#each $templateConfig.template.ports as port}
          <div class="grid grid-cols-12 gap-3 items-end">
            <div class="col-span-5">
              <label class="block text-xs font-medium text-zinc-400 mb-1">Host Port</label>
              <input 
                type="number" 
                bind:value={$templateConfig.ports[port.container_port]}
                min="1" max="65535"
                class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
              />
            </div>
            <div class="col-span-2 text-center text-zinc-600 pb-2">→</div>
            <div class="col-span-5">
              <label class="block text-xs font-medium text-zinc-400 mb-1">Container</label>
              <input 
                type="number" 
                disabled 
                value={port.container_port}
                class="w-full px-3 py-2 bg-zinc-950/50 border border-zinc-800 rounded-sm text-sm text-zinc-500 font-mono"
              />
            </div>
          </div>
        {/each}
      </div>
    {:else if steps[currentStep] === 'network'}
      <h3 class="text-lg font-semibold text-white mb-4">Network Configuration</h3>
      <div class="space-y-3">
        <div>
          <label class="block text-xs font-medium text-zinc-400 mb-1">Network Mode</label>
          <select 
            bind:value={$templateConfig.networkMode}
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white">
            <option value="bridge">bridge (default)</option>
            <option value="host">host</option>
            <option value="none">none (isolated)</option>
            <option value="custom">custom network</option>
          </select>
        </div>

        {#if $templateConfig.networkMode === 'custom'}
          <div>
            <label class="block text-xs font-medium text-zinc-400 mb-1">Custom Network Name</label>
            <input 
              type="text" 
              bind:value={$templateConfig.customNetwork}
              placeholder="my-network"
              class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
            />
          </div>
        {/if}
      </div>
    {:else if steps[currentStep] === 'advanced'}
      <h3 class="text-lg font-semibold text-white mb-4">Advanced Options</h3>
      <div class="space-y-3">
        <div>
          <label class="block text-xs font-medium text-zinc-400 mb-1">Restart Policy</label>
          <select 
            bind:value={$templateConfig.restartPolicy}
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white">
            <option value="unless-stopped">unless-stopped (recommended)</option>
            <option value="always">always</option>
            <option value="on-failure">on-failure</option>
            <option value="no">no</option>
          </select>
        </div>

        <label class="flex items-center gap-3">
          <input 
            type="checkbox" 
            bind:checked={$templateConfig.autoRecover}
            class="w-4 h-4 rounded bg-zinc-950 border-zinc-700"
          />
          <div>
            <div class="text-sm font-medium text-white">Auto-recovery</div>
            <div class="text-xs text-zinc-500">Automatically restart container if it exits unexpectedly</div>
          </div>
        </label>
      </div>
    {/if}
  </div>

  <!-- Navigation buttons -->
  <div class="flex justify-between gap-3">
    <Button 
      variant="secondary" 
      disabled={currentStep === 0 || deploying}
      on:click={prevStep}>
      Previous
    </Button>

    {#if currentStep === steps.length - 1}
      <Button 
        variant="primary" 
        loading={deploying}
        on:click={deploy}>
        Deploy
      </Button>
    {:else}
      <Button 
        variant="primary"
        disabled={deploying}
        on:click={nextStep}>
        Next
      </Button>
    {/if}
  </div>
</div>
```

---

### 2.4 Forms & Validation

#### Create reusable form field component

```svelte
<!-- src/components/Form/Input.svelte -->
<script lang="ts">
  interface Props {
    label: string;
    hint?: string;
    error?: string;
    required?: boolean;
    type?: 'text' | 'password' | 'email' | 'number' | 'url';
    placeholder?: string;
    disabled?: boolean;
    name: string;
  }

  let value = '';

  let {
    label,
    hint,
    error,
    required = false,
    type = 'text',
    placeholder,
    disabled = false,
    name
  }: Props = $props();
</script>

<div>
  <label for={name} class="block text-xs font-medium text-zinc-400 mb-1">
    {label}{#if required}<span class="text-red-400">*</span>{/if}
  </label>
  <input 
    id={name}
    type={type}
    bind:value
    {placeholder}
    disabled={disabled}
    name={name}
    class="w-full px-3 py-2 bg-zinc-950 border rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600 disabled:opacity-50 disabled:cursor-not-allowed {error ? 'border-red-500' : 'border-zinc-800'}"
  />
  {#if hint && !error}
    <p class="text-xs text-zinc-600 mt-1">{hint}</p>
  {/if}
  {#if error}
    <p class="text-xs text-red-400 mt-1">{error}</p>
  {/if}
</div>
```

---

### 2.5 Modal & Dialog Components

```svelte
<!-- src/components/UI/Modal.svelte -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  interface Props {
    open: boolean;
    title: string;
    size?: 'sm' | 'md' | 'lg' | 'xl';
  }

  let { open, title, size = 'md' }: Props = $props();

  const dispatch = createEventDispatcher<{
    close: void;
    confirm: unknown;
  }>();

  function handleOverlayClick(event: MouseEvent) {
    if ((event.target as HTMLElement).id === 'modal-overlay') {
      dispatch('close');
    }
  }

  const sizeClasses = {
    sm: 'max-w-md',
    md: 'max-w-lg',
    lg: 'max-w-2xl',
    xl: 'max-w-4xl'
  };
</script>

{#if open}
  <div 
    id="modal-overlay"
    class="fixed inset-0 bg-black/50 z-50 flex items-center justify-center p-4"
    on:click={handleOverlayClick}>
    <div class="bg-zinc-900 border border-zinc-800 rounded-sm w-full {sizeClasses[size]} animate-in fade-in zoom-in-95 duration-200">
      <div class="flex items-center justify-between p-4 border-b border-zinc-800">
        <h3 class="text-lg font-semibold text-white">{title}</h3>
        <button 
          class="text-zinc-400 hover:text-white"
          on:click={() => dispatch('close')}>
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
          </svg>
        </button>
      </div>
      
      <div class="p-4">
        <slot />
      </div>
    </div>
  </div>
{/if}
```

---

## Deliverables Checklist

- [ ] ✅ Button component (primary, secondary, danger variants)
- [ ] ✅ Dashboard card component with icons and trends
- [ ] ✅ Service table with status indicators
- [ ] ✅ Multi-step deployment wizard
- [ ] ✅ Form input component with validation states
- [ ] ✅ Modal/dialog component with overlay
- [ ] ✅ Alert/toast notification component
- [ ] ✅ Tabs navigation component
- [ ] ✅ Input group and combo components
- [ ] ✅ Icon component library

## Testing Requirements

- Unit tests for all components using Vitest
- E2E test for deployment flow using Playwright
- Accessibility audit (WCAG AA compliance)
- Responsive design verification (mobile, tablet, desktop)

---

**Next step:** Proceed to **Phase 3: Authentication & Routing** once component library is complete.
