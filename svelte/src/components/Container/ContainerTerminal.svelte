<script lang="ts">
  // Interactive terminal (docker exec) over the instance exec WebSocket.
  // Browser WS → server → TerminalBridge → container TTY.
  //
  // The terminal is intentionally simple (no xterm.js dependency): output is
  // rendered as plain text in a scrollable pre, input is sent per keystroke
  // in a hidden input. ANSI colors show as escape codes; full xterm support
  // can be layered in later without touching the backend.
  import { onDestroy } from 'svelte';
  import { getToken } from '$lib/api/client';

  interface Props {
    instanceId: string;
    containerId: string;
    running: boolean;
    shell?: string;
  }
  let { instanceId, containerId, running, shell = 'sh' }: Props = $props();

  let output = $state('');
  let connected = $state(false);
  let status = $state<'idle' | 'connecting' | 'open' | 'closed' | 'error'>('idle');
  let statusMessage = $state('');

  let socket: WebSocket | null = null;
  let outEl: HTMLPreElement | undefined = $state();
  let inputEl: HTMLInputElement | undefined = $state();

  function wsURL(): string {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${proto}//${location.host}/api/instances/${encodeURIComponent(instanceId)}/containers/${encodeURIComponent(containerId)}/exec?shell=${shell}&token=${encodeURIComponent(getToken() ?? '')}`;
  }

  function connect() {
    if (!running) {
      status = 'error';
      statusMessage = 'Container is not running — start it to open a terminal.';
      return;
    }
    socket?.close();
    output = '';
    status = 'connecting';
    statusMessage = '';

    socket = new WebSocket(wsURL());
    socket.binaryType = 'arraybuffer';

    socket.onopen = () => {
      status = 'open';
      connected = true;
      inputEl?.focus();
    };
    socket.onmessage = (event) => {
      let text: string;
      if (event.data instanceof ArrayBuffer) {
        text = new TextDecoder().decode(event.data);
      } else {
        text = String(event.data);
      }
      output += text;
      if (output.length > 400_000) output = output.slice(-400_000);
      if (outEl) outEl.scrollTop = outEl.scrollHeight;
    };
    socket.onclose = () => {
      connected = false;
      if (status !== 'error') {
        status = 'closed';
        statusMessage = 'Session closed.';
      }
    };
    socket.onerror = () => {
      connected = false;
      status = 'error';
      statusMessage = 'Connection failed.';
    };
  }

  function send(payload: string) {
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(payload);
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (!connected) return;
    // Convert special keys to their TTY control sequences so shell
    // line-editing (arrows, history, backspace) works.
    const map: Record<string, string> = {
      ArrowUp: '\x1b[A', ArrowDown: '\x1b[B', ArrowRight: '\x1b[C', ArrowLeft: '\x1b[D',
      Home: '\x1b[H', End: '\x1b[F', Delete: '\x1b[3~', Tab: '\t', Escape: '\x1b'
    };
    if (e.key.length === 1 && e.ctrlKey && (e.key === 'c' || e.key === 'd')) return;
    if (map[e.key]) {
      e.preventDefault();
      send(map[e.key]);
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      send('\r');
      return;
    }
    if (e.key === 'Backspace') {
      e.preventDefault();
      send('\x7f');
      return;
    }
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      send(e.key);
    }
  }

  function handleCtrlC() { send('\x03'); }
  function handleCtrlD() { send('\x04'); }

  onDestroy(() => {
    socket?.close();
    socket = null;
  });
</script>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-3">
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div class="flex items-center gap-2 text-xs text-zinc-500">
      <span
        class={`w-2 h-2 rounded-full ${connected ? 'bg-emerald-400' : status === 'error' ? 'bg-red-400' : 'bg-zinc-600'}`}
        aria-hidden="true"
      ></span>
      {#if status === 'open'}connected · {shell}{:else}{statusMessage || status}{/if}
    </div>
    <button
      class="px-3 py-1.5 text-xs font-medium rounded-sm bg-white hover:bg-zinc-200 text-zinc-900 transition-colors"
      onclick={connect}
    >
      {status === 'open' ? 'Restart session' : 'Open terminal'}
    </button>
  </div>

  {#if status === 'open' || status === 'closed'}
    <pre
      bind:this={outEl}
      class="bg-black border border-zinc-800 rounded-sm p-3 text-xs font-mono text-zinc-300 overflow-auto h-[420px] whitespace-pre-wrap break-all"
    >{output || '—'}</pre>

    {#if connected}
      <!-- Hidden input captures keystrokes; the visible line is a hint. -->
      <input
        bind:this={inputEl}
        class="sr-only"
        aria-label="Terminal input"
        onkeydown={onKeydown}
      />
      <p class="text-[11px] text-zinc-600">
        Type while this panel is focused — input is sent per keystroke. Ctrl+C interrupts, Ctrl+D exits, arrows/editing work.
      </p>
      <div class="flex gap-2">
        <button class="px-2 py-1 text-[11px] rounded-sm border border-zinc-700 text-zinc-400 hover:text-white hover:bg-zinc-800" onclick={handleCtrlC}>Ctrl+C</button>
        <button class="px-2 py-1 text-[11px] rounded-sm border border-zinc-700 text-zinc-400 hover:text-white hover:bg-zinc-800" onclick={handleCtrlD}>Ctrl+D</button>
      </div>
    {/if}
  {:else if status === 'error'}
    <div class="bg-black border border-zinc-800 rounded-sm p-4 text-xs font-mono text-red-400 h-[420px] overflow-auto whitespace-pre-wrap">
      {statusMessage}
    </div>
  {:else}
    <div class="bg-black border border-zinc-800 rounded-sm p-4 text-xs font-mono text-zinc-600 flex items-center justify-center h-[420px]">
      Terminal not started — click "Open terminal" to run <span class="text-zinc-400 mx-1">{shell}</span> in this container.
    </div>
  {/if}
</div>

<svelte:document on:click={(e) => {
  if (connected && inputEl && !(e.target as HTMLElement).closest('button, select, a')) {
    inputEl.focus();
  }
}} />
