<script lang="ts">
  // Live log viewer for a container — streams over the existing WS endpoint
  // GET /api/instances/:id/containers/:id/logs?tail=N&token=...
  import { onDestroy } from 'svelte';
  import { getToken } from '$lib/api/client';
  import Icon from '../ui/Icon.svelte';

  interface Props {
    instanceId: string;
    containerId: string;
    running: boolean;
  }
  let { instanceId, containerId, running }: Props = $props();

  let logs = $state<string>('');
  let connected = $state(false);
  let follow = $state(true);
  let tail = $state<'100' | '500' | '2000'>('500');
  let wrapLines = $state(true);

  let socket: WebSocket | null = null;
  let logEl: HTMLPreElement | undefined = $state();
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  // Snapshot at connect() time — compare inside a closure so the warning
  // about capturing initial props is intentional.
  let lastInstanceId = '';
  let lastContainerId = '';

  function wsURL(tailN: string): string {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${proto}//${location.host}/api/instances/${encodeURIComponent(instanceId)}/containers/${encodeURIComponent(containerId)}/logs?tail=${tailN}&token=${encodeURIComponent(getToken() ?? '')}`;
  }

  function connect() {
    socket?.close();
    socket = null;
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    lastInstanceId = instanceId;
    lastContainerId = containerId;

    socket = new WebSocket(wsURL(tail));
    socket.onopen = () => { connected = true; };
    socket.onmessage = (event) => {
      // Docker multiplexes stdout/stderr; the server already demuxes, so each
      // message is plain text. Append with a newline boundary.
      logs += (logs && !logs.endsWith('\n') ? '\n' : '') + String(event.data);
      // Cap the buffer: keep the last ~400KB so a chatty container can't
      // grow the DOM without bound.
      if (logs.length > 400_000) logs = logs.slice(-400_000);
      if (follow && logEl) logEl.scrollTop = logEl.scrollHeight;
    };
    socket.onclose = () => {
      connected = false;
      // Auto-reconnect only while following a running container.
      if (follow && running && instanceId === lastInstanceId && containerId === lastContainerId) {
        reconnectTimer = setTimeout(connect, 3000);
      }
    };
    socket.onerror = () => { connected = false; };
  }

  function restartStream() {
    logs = '';
    connect();
  }

  // Reconnect when the container or instance changes (parent remounts by key,
  // but guard anyway).
  $effect(() => {
    void containerId;
    void instanceId;
    if (containerId !== lastContainerId || instanceId !== lastInstanceId) {
      restartStream();
    }
  });

  onDestroy(() => {
    socket?.close();
    socket = null;
    if (reconnectTimer) clearTimeout(reconnectTimer);
  });
</script>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-3">
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div class="flex items-center gap-2">
      <span
        class={`w-2 h-2 rounded-full ${connected ? 'bg-emerald-400' : 'bg-zinc-600'}`}
        aria-hidden="true"
      ></span>
      <span class="text-xs text-zinc-500">{connected ? 'streaming' : 'disconnected'}</span>
    </div>
    <div class="flex items-center gap-2 flex-wrap">
      <label class="flex items-center gap-1.5 text-xs text-zinc-400">
        <input type="checkbox" bind:checked={follow} class="accent-blue-600" />
        Follow
      </label>
      <label class="flex items-center gap-1.5 text-xs text-zinc-400">
        <input type="checkbox" bind:checked={wrapLines} class="accent-blue-600" />
        Wrap
      </label>
      <select
        bind:value={tail}
        onchange={restartStream}
        class="bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-zinc-200 px-2 py-1 focus:outline-none"
        aria-label="Tail lines"
      >
        <option value="100">last 100</option>
        <option value="500">last 500</option>
        <option value="2000">last 2000</option>
      </select>
      <button
        class="p-1.5 rounded-sm text-zinc-400 hover:text-white hover:bg-zinc-800 transition-colors"
        title="Restart stream"
        aria-label="Restart log stream"
        onclick={restartStream}
      >
        <Icon name="restart" class="w-4 h-4" />
      </button>
    </div>
  </div>

  <pre
    bind:this={logEl}
    class={`bg-black border border-zinc-800 rounded-sm p-3 text-xs font-mono text-zinc-300 overflow-auto h-[420px] ${wrapLines ? 'whitespace-pre-wrap break-all' : 'whitespace-pre overflow-x-auto'}`}
  >{logs || (running ? 'Waiting for output…' : 'Container is not running — logs shown are from the last run.')}</pre>
</div>
