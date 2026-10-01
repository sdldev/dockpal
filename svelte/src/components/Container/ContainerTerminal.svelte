<script lang="ts">
  // Interactive terminal (docker exec) over the instance exec WebSocket,
  // rendered with xterm.js. The emulator interprets the container's TTY byte
  // stream (colors, cursor moves, escape sequences) and answers terminal
  // queries itself, so raw escapes like the cursor-position report never leak
  // onto the screen. Input goes straight from the emulator to the WS as
  // binary frames; size changes are sent as JSON control frames that the
  // server applies with docker exec resize.
  import { onDestroy, tick } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import '@xterm/xterm/css/xterm.css';
  import { getWSTicket } from '$lib/api/containers';

  interface Props {
    instanceId: string;
    containerId: string;
    running: boolean;
    shell?: string;
  }
  let { instanceId, containerId, running, shell = 'sh' }: Props = $props();

  let connected = $state(false);
  let status = $state<'idle' | 'connecting' | 'open' | 'closed' | 'error'>('idle');
  let statusMessage = $state('');

  let socket: WebSocket | null = null;
  let term: Terminal | null = null;
  let fitAddon: FitAddon | null = null;
  let termEl: HTMLDivElement | undefined = $state();

  function wsURL(ticket: string): string {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${proto}//${location.host}/api/instances/${encodeURIComponent(instanceId)}/containers/${encodeURIComponent(containerId)}/exec?shell=${shell}&token=${encodeURIComponent(ticket)}`;
  }

  function sendResize(cols: number, rows: number) {
    if (socket?.readyState !== WebSocket.OPEN) return;
    socket.send(JSON.stringify({ type: 'resize', cols, rows }));
  }

  async function connect() {
    if (!running) {
      status = 'error';
      statusMessage = 'Container is not running — start it to open a terminal.';
      return;
    }
    teardown();
    status = 'connecting';
    statusMessage = '';

    term = new Terminal({
      cursorBlink: true,
      scrollback: 5000,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 12,
      theme: {
        background: '#000000',
        foreground: '#d4d4d4',
        cursor: '#d4d4d4',
        selectionBackground: '#264f78'
      }
    });
    fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.onData((data) => {
      // emulator input → container stdin (binary frame). Drop input while the
      // socket isn't OPEN — xterm keeps firing onData on keystrokes, and a
      // send on a closing/closed socket throws into the console.
      if (socket?.readyState !== WebSocket.OPEN) return;
      socket.send(new TextEncoder().encode(data));
    });
    term.onResize(({ cols, rows }) => sendResize(cols, rows));

    // Single-use 60s ticket instead of the 4h JWT in the URL (audit L1).
    const ticket = await getWSTicket().catch(() => '');
    if (!ticket) {
      status = 'error';
      statusMessage = 'Could not obtain a session ticket — please retry.';
      return;
    }
    socket = new WebSocket(wsURL(ticket));
    socket.binaryType = 'arraybuffer';
    socket.onopen = async () => {
      status = 'open';
      connected = true;
      // the terminal container only renders once status flips, so wait for
      // the DOM update before attaching the emulator.
      await tick();
      if (term && termEl) {
        term.open(termEl);
        try {
          fitAddon?.fit(); // triggers onResize → server sets the exec TTY size
        } catch {
          // container not measurable yet (hidden tab); the next resize refits
        }
        term.focus();
      }
    };
    socket.onmessage = (event) => {
      if (!term) return;
      // container TTY output → emulator (handles escapes, answers DSR, etc.)
      term.write(event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : event.data);
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

  function teardown() {
    socket?.close();
    socket = null;
    term?.dispose();
    term = null;
    fitAddon = null;
  }

  function sendControlByte(b: string) {
    socket?.send(new TextEncoder().encode(b));
  }

  onDestroy(teardown);

  // Refit when the panel is resized so the emulator and the container TTY
  // stay in sync (fit triggers onResize, which notifies the server).
  $effect(() => {
    if (!termEl || !fitAddon) return;
    const observer = new ResizeObserver(() => {
      try {
        fitAddon?.fit();
      } catch {
        // not measurable; keep the last size
      }
    });
    observer.observe(termEl);
    return () => observer.disconnect();
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
    <!-- xterm.js mounts here; background/foreground come from the theme above -->
    <div bind:this={termEl} class="bg-black border border-zinc-800 rounded-sm p-2 h-[420px] overflow-hidden"></div>
    {#if connected}
      <div class="flex gap-2">
        <button
          class="px-2 py-1 text-[11px] rounded-sm border border-zinc-700 text-zinc-400 hover:text-white hover:bg-zinc-800"
          onclick={() => sendControlByte('\x03')}>Ctrl+C</button>
        <button
          class="px-2 py-1 text-[11px] rounded-sm border border-zinc-700 text-zinc-400 hover:text-white hover:bg-zinc-800"
          onclick={() => sendControlByte('\x04')}>Ctrl+D</button>
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
