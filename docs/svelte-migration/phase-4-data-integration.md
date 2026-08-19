# Phase 4: Data Integration & Real-time Updates

## Overview
Connect Svelte components to Go backend APIs. Implement real-time updates via WebSocket/SSE for containers, logs, and deployment progress.

## Timeline
**Duration:** 4-5 days
**Priority:** High

---

## Task List

### 4.1 API Type Definitions

#### Create `src/lib/types/generated.ts`

```typescript
// Auto-generated types from OpenAPI spec or manual definitions

export interface Service {
  id: string;
  name: string;
  status: 'running' | 'stopped' | 'degraded' | 'error';
  type: 'container' | 'compose' | 'git' | 'template';
  instance_id?: string;
  ports: Array<{ label: string; host_port: number; container_port: number }>;
  environment: Record<string, string>;
  domains?: string[];
  created_at: Date;
  updated_at: Date;
}

export interface ContainerStats {
  cpu_percent: number;
  memory_usage: number;
  memory_limit: number;
  memory_percent: number;
  network_io: { bytes_sent: number; bytes_received: number };
  block_io: { bytes_written: number; bytes_read: number };
  pids_count: number;
}

export interface DeployLog {
  time: string;
  step: 'start' | 'pull' | 'create' | 'deploy' | 'done' | 'hint';
  message: string;
  status?: 'pending' | 'error' | 'done';
  stream?: boolean;
}

export interface Image {
  id: string;
  repository: string;
  tag: string;
  size: number;
  created_at: Date;
  platforms?: string[];
  digest: string;
}

export interface HealthStatus {
  status: 'healthy' | 'unhealthy';
  components: {
    database: 'ok' | 'error';
    docker: 'ok' | 'error';
    agent?: 'ok' | 'error' | 'offline';
  };
  version: string;
  uptime_seconds: number;
}

export interface AuditEntry {
  id: string;
  timestamp: Date;
  user_id: string;
  username: string;
  action: string;
  resource: string;
  details: Record<string, unknown>;
  ip_address: string;
}
```

---

### 4.2 Service Store with Polling

#### Extend `$lib/store.ts`

```typescript
import { writable, derived, readonly } from 'svelte/store';

// Services store with auto-refresh
export function createServicesStore() {
  let pollingInterval: NodeJS.Timeout | null = null;
  let lastFetched: Date | null = null;
  
  const { subscribe, update, set } = writable<Service[]>([]);

  async function fetchServices() {
    try {
      const response = await fetch('/api/services');
      if (!response.ok) throw new Error('Failed to fetch services');
      
      const data = await response.json();
      set(data);
      lastFetched = new Date();
    } catch (error) {
      console.error('Service polling failed:', error);
    }
  }

  function startPolling(intervalMs = 5000) {
    if (pollingInterval) return;
    
    fetchServices(); // Initial fetch
    
    pollingInterval = setInterval(() => {
      fetchServices();
    }, intervalMs);
  }

  function stopPolling() {
    if (pollingInterval) {
      clearInterval(pollingInterval);
      pollingInterval = null;
    }
  }

  // Derived store: filtered services
  const runningServices = derived(subscribe, $services => 
    $services.filter(s => s.status === 'running')
  );

  const stoppedServices = derived(subscribe, $services => 
    $services.filter(s => s.status === 'stopped')
  );

  return {
    subscribe,
    fetchServices,
    startPolling,
    stopPolling,
    startPollingDefault: () => startPolling(5000),
    get lastFetched() {
      return lastFetched;
    }
  };
}

export const servicesStore = createServicesStore();
```

---

### 4.3 WebSocket Stream Client

#### Create `src/lib/api/stream.ts`

```typescript
/**
 * Real-time event streaming using WebSocket
 * Supports deployments, logs, stats updates
 */

export interface StreamMessage<T = unknown> {
  type: string;
  data: T;
  timestamp: Date;
}

export class StreamClient {
  private ws: WebSocket | null = null;
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 5;
  private reconnectDelay = 1000;
  private url: string;
  private onMessageHandlers: Set<(msg: StreamMessage) => void> = new Set();
  private onCloseHandlers: Set<() => void> = new Set();
  private onErrorHandlers: Set<(error: Event) => void> = new Set();

  constructor(baseUrl: string = '/api') {
    this.url = `${baseUrl}`;
  }

  connect(token?: string) {
    const queryString = token ? `?token=${token}` : '';
    const wsUrl = `ws://${window.location.host}/ws/stream${queryString}`;
    
    this.ws = new WebSocket(wsUrl);

    this.ws.onopen = () => {
      console.log('Stream connected');
      this.reconnectAttempts = 0;
    };

    this.ws.onmessage = (event) => {
      try {
        const message: StreamMessage = JSON.parse(event.data);
        this.onMessageHandlers.forEach(handler => handler(message));
      } catch (error) {
        console.error('Stream parse error:', error);
      }
    };

    this.ws.onclose = (event) => {
      console.log('Stream disconnected:', event.code);
      this.onCloseHandlers.forEach(handler => handler());
      
      if (event.code !== 1000 && this.reconnectAttempts < this.maxReconnectAttempts) {
        this.scheduleReconnect();
      }
    };

    this.ws.onerror = (error) => {
      this.onErrorHandlers.forEach(handler => handler(error));
    };
  }

  disconnect() {
    if (this.ws) {
      this.ws.close(1000, 'User disconnected');
      this.ws = null;
    }
  }

  subscribe(
    onMessage: (msg: StreamMessage) => void,
    onClose?: () => void,
    onError?: (error: Event) => void
  ) {
    this.onMessageHandlers.add(onMessage);
    if (onClose) this.onCloseHandlers.add(onClose);
    if (onError) this.onErrorHandlers.add(onError);
  }

  unsubscribe(
    onMessage?: (msg: StreamMessage) => void,
    onClose?: () => void,
    onError?: (error: Event) => void
  ) {
    if (onMessage) this.onMessageHandlers.delete(onMessage);
    if (onClose) this.onCloseHandlers.delete(onClose);
    if (onError) this.onErrorHandlers.delete(onError);
  }

  scheduleReconnect() {
    this.reconnectAttempts++;
    const delay = Math.min(this.reconnectDelay * this.reconnectAttempts, 30000);
    
    setTimeout(() => {
      console.log(`Attempting reconnect ${this.reconnectAttempts}/${this.maxReconnectAttempts}`);
      const token = localStorage.getItem('dockpal_token');
      this.connect(token || undefined);
    }, delay);
  }
}

export const streamClient = new StreamClient();
```

---

### 4.4 Deployment Log Component

#### Create `src/components/Deploy/LogViewer.svelte`

```svelte
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, streamClient } from '$lib/api/client';
  import { deploySession } from '$lib/store';

  export let deployId: string;

  let logs: Array<{ time: string; step: string; message: string; status?: string }> = [];
  let isComplete = false;

  function formatTimestamp(dateStr: string): string {
    const date = new Date(dateStr);
    return date.toLocaleTimeString('en-US', { hour12: false });
  }

  function handleStreamMessage(msg: any) {
    const logEntry = {
      time: formatTimestamp(msg.timestamp || new Date().toISOString()),
      step: msg.step || 'info',
      message: msg.message || '',
      status: msg.status || 'pending'
    };

    logs = [...logs, logEntry];

    if (msg.type === 'complete' || msg.type === 'error') {
      isComplete = true;
      streamClient.disconnect();
    }
  }

  async function loadInitialLogs() {
    try {
      const response = await fetch(`/api/deploy/stream/${deployId}`);
      if (response.ok) {
        // Fetch initial state
        const data = await response.json();
        logs = data.logs || [];
        isComplete = data.complete || false;
        
        // Subscribe to live updates
        const token = localStorage.getItem('dockpal_token');
        streamClient.connect(token);
        streamClient.subscribe(handleStreamMessage);
      }
    } catch (error) {
      console.error('Failed to load deployment logs:', error);
    }
  }

  onMount(async () => {
    await loadInitialLogs();
  });

  onDestroy(() => {
    streamClient.unsubscribe(handleStreamMessage);
  });
</script>

<div class="bg-zinc-950 border border-zinc-800 rounded-sm p-4 font-mono text-xs h-full overflow-auto">
  {#each logs as log (log.time + log.message)}
    <div class="flex items-start gap-2 mb-1 last:mb-0">
      <span class="text-zinc-600 shrink-0">{log.time}</span>
      <span 
        class="{
          log.status === 'error' ? 'text-red-400' :
          log.status === 'done' ? 'text-emerald-400' :
          log.step === 'hint' ? 'text-amber-400' :
          'text-blue-400'
        }">
        {log.message}
      </span>
    </div>
  {/each}

  {#if !isComplete && logs.length === 0}
    <div class="text-zinc-500 text-center py-8">Loading deployment logs...</div>
  {/if}

  {#if isComplete && logs.length > 0}
    <div class="mt-4 pt-4 border-t border-zinc-800">
      <button 
        onclick="window.location.href='/containers'"
        class="text-sm text-blue-400 hover:text-blue-300">
        View deployed containers →
      </button>
    </div>
  {/if}
</div>
```

---

### 4.5 Container Stats Chart

#### Create `src/components/Container/StatsChart.svelte`

```svelte
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { chart } from 'chart.js/auto';
  import { registerables } from 'chart.js';
  import type { ContainerStats } from '$lib/types/generated';

  chart.register(...registerables);

  export let containerId: string;

  let chartInstance: chart | null = null;
  let stats: ContainerStats | null = null;
  let polling = true;

  async function fetchStats() {
    try {
      const response = await fetch(`/api/container/${containerId}/stats`);
      if (response.ok) {
        stats = await response.json();
      }
    } catch (error) {
      console.error('Failed to fetch stats:', error);
    }
  }

  async function renderChart(statsList: ContainerStats[]) {
    const ctx = document.getElementById('stats-chart') as HTMLCanvasElement;
    
    if (chartInstance) {
      chartInstance.destroy();
    }

    chartInstance = new chart(ctx, {
      type: 'line',
      data: {
        labels: statsList.map(s => s.timestamp),
        datasets: [
          {
            label: 'CPU %',
            data: statsList.map(s => s.cpu_percent),
            borderColor: '#3b82f6',
            backgroundColor: 'rgba(59, 130, 246, 0.1)',
            tension: 0.4,
            fill: true
          },
          {
            label: 'Memory MB',
            data: statsList.map(s => s.memory_usage / 1024 / 1024),
            borderColor: '#10b981',
            backgroundColor: 'rgba(16, 185, 129, 0.1)',
            tension: 0.4,
            fill: true
          }
        ]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { display: true },
          tooltip: { mode: 'index', intersect: false }
        },
        scales: {
          x: { display: true, grid: { display: false } },
          y: { display: true, beginAtZero: true, grid: { color: '#27272a' } }
        }
      }
    });
  }

  onMount(async () => {
    // Initial fetch
    await fetchStats();
    
    // Start polling every 2 seconds
    const interval = setInterval(fetchStats, 2000);
    
    return () => {
      clearInterval(interval);
      if (chartInstance) chartInstance.destroy();
    };
  });
</script>

<div class="space-y-4">
  <canvas id="stats-chart" height="250"></canvas>
  
  {#if stats}
    <div class="grid grid-cols-2 gap-4 text-sm">
      <div class="bg-zinc-900 p-3 rounded-sm">
        <div class="text-zinc-500 text-xs mb-1">CPU Usage</div>
        <div class="text-lg font-semibold">{stats.cpu_percent.toFixed(1)}%</div>
      </div>
      <div class="bg-zinc-900 p-3 rounded-sm">
        <div class="text-zinc-500 text-xs mb-1">Memory</div>
        <div class="text-lg font-semibold">
          {(stats.memory_usage / 1024 / 1024).toFixed(1)} MB / {(stats.memory_limit / 1024 / 1024).toFixed(1)} MB
        </div>
      </div>
    </div>
  {/if}
</div>
```

---

### 4.6 Search & Filter Utilities

#### Create `src/lib/utils/search.ts`

```typescript
export interface SearchResult<T> {
  results: T[];
  total: number;
  page: number;
  limit: number;
}

export function debounce<T extends (...args: unknown[]) => unknown>(
  func: T,
  wait: number
): (...args: Parameters<T>) => void {
  let timeout: ReturnType<typeof setTimeout> | null = null;

  return (...args: Parameters<T>) => {
    if (timeout) clearTimeout(timeout);
    timeout = setTimeout(() => func(...args), wait);
  };
}

export function filterByQuery<T>(
  items: T[],
  query: string,
  fields: (keyof T)[],
  limit: number = 100
): T[] {
  const lowerQuery = query.toLowerCase().trim();
  
  if (!lowerQuery) return items.slice(0, limit);

  return items
    .filter(item => {
      for (const field of fields) {
        const value = item[field];
        if (typeof value === 'string' && value.toLowerCase().includes(lowerQuery)) {
          return true;
        }
        if (Array.isArray(value) && value.some(v => String(v).toLowerCase().includes(lowerQuery))) {
          return true;
        }
      }
      return false;
    })
    .slice(0, limit);
}

export function sortByField<T>(
  items: T[],
  field: keyof T,
  order: 'asc' | 'desc' = 'asc'
): T[] {
  return [...items].sort((a, b) => {
    const aValue = a[field];
    const bValue = b[field];

    if (aValue == null && bValue != null) return 1;
    if (aValue != null && bValue == null) return -1;
    if (aValue == null && bValue == null) return 0;

    const comparison = String(aValue).localeCompare(String(bValue));
    return order === 'asc' ? comparison : -comparison;
  });
}
```

---

## Deliverables Checklist

- [ ] ✅ Service store with polling mechanism
- [ ] ✅ WebSocket stream client for real-time events
- [ ] ✅ Deployment log viewer component
- [ ] ✅ Container stats chart (CPU/memory visualization)
- [ ] ✅ Search & filter utility functions
- [ ] ✅ Auto-refresh intervals for all data sources
- [ ] ✅ Error handling & retry logic
- [ ] ✅ Empty states & loading indicators

---

**Next step:** Proceed to **Phase 5: Build Pipeline & Production Deployment** once data integration is complete.
