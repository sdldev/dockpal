<script lang="ts">
  // One-shot "Run Command" panel — the non-interactive counterpart of the
  // xterm terminal. It works on every transport, including edge agents
  // (interactive exec is unavailable there), by POSTing the command to
  // /containers/:id/exec and showing the captured output and exit code.
  // The command runs without a TTY and must finish on its own (30s default,
  // 120s max), so REPLs and tail-style commands are intentionally out of
  // scope for this panel.
  import { api } from '$lib/api/client';
  import { containersBasePath } from '$lib/api/containers';

  interface Props {
    instanceId: string;
    containerId: string;
    running: boolean;
  }
  let { instanceId, containerId, running }: Props = $props();

  let command = $state('');
  let running2 = $state(false); // request in flight
  let result = $state<{ exit_code: number; stdout: string; stderr: string; timed_out?: boolean; truncated?: boolean; duration_ms: number } | null>(null);
  let error = $state('');

  const basePath = $derived(containersBasePath(instanceId));

  // The input accepts shell-style text; it is split into argv here (simple
  // whitespace split honoring double/single quotes) so the endpoint keeps its
  // exec-form contract and no shell interpolation happens agent-side.
  function parseArgs(input: string): string[] {
    const args: string[] = [];
    const re = /"([^"]*)"|'([^']*)'|(\S+)/g;
    let m: RegExpExecArray | null;
    while ((m = re.exec(input)) !== null) {
      args.push(m[1] ?? m[2] ?? m[3]);
    }
    return args;
  }

  async function run() {
    const cmd = parseArgs(command.trim());
    if (cmd.length === 0) {
      error = 'Enter a command first.';
      return;
    }
    running2 = true;
    error = '';
    result = null;
    try {
      result = await api.post<{ exit_code: number; stdout: string; stderr: string; timed_out?: boolean; truncated?: boolean; duration_ms: number }>(
        `${basePath}/${encodeURIComponent(containerId)}/exec`,
        { cmd }
      );
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      // Older agent images have no exec endpoint — surface the upgrade hint.
      if (error.includes('404')) {
        error = 'This server\'s agent image does not support Run Command yet — update the agent image on that server.';
      }
    } finally {
      running2 = false;
    }
  }
</script>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-3">
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div>
      <h3 class="text-sm font-medium text-white">Run Command</h3>
      <p class="text-xs text-zinc-500 mt-0.5">
        One-shot, non-interactive — works on every server, including edge agents. 30s timeout (max 120s).
      </p>
    </div>
  </div>

  {#if !running}
    <p class="text-xs text-zinc-600">Container is not running — start it to run commands.</p>
  {:else}
    <form
      class="flex gap-2"
      onsubmit={(e) => { e.preventDefault(); void run(); }}
    >
      <input
        type="text"
        bind:value={command}
        placeholder='e.g. ls -la /tmp  ·  /usr/local/bin/setup email add user@example.com'
        aria-label="Command to run"
        class="flex-1 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono placeholder:text-zinc-600 focus:outline-none focus:border-zinc-600"
      />
      <button
        type="submit"
        disabled={running2 || command.trim().length === 0}
        class="px-4 py-2 text-xs font-medium rounded-sm bg-white hover:bg-zinc-200 disabled:opacity-40 disabled:cursor-not-allowed text-zinc-900 transition-colors"
      >
        {running2 ? 'Running…' : 'Run'}
      </button>
    </form>

    {#if error}
      <p class="text-xs text-red-400" role="alert">{error}</p>
    {/if}

    {#if result}
      <div class="space-y-2">
        <div class="flex items-center gap-3 text-xs flex-wrap">
          <span class={result.exit_code === 0 ? 'text-emerald-400' : 'text-red-400'}>
            exit {result.exit_code}
          </span>
          <span class="text-zinc-600">{result.duration_ms} ms</span>
          {#if result.timed_out}<span class="text-amber-400">timed out</span>{/if}
          {#if result.truncated}<span class="text-amber-400">output truncated</span>{/if}
        </div>
        {#if result.stdout}
          <pre class="bg-zinc-950 border border-zinc-800 rounded-sm p-3 text-xs font-mono text-zinc-300 overflow-auto max-h-72 whitespace-pre-wrap">{result.stdout}</pre>
        {/if}
        {#if result.stderr}
          <pre class="bg-zinc-950 border border-zinc-800 rounded-sm p-3 text-xs font-mono text-red-400 overflow-auto max-h-72 whitespace-pre-wrap">{result.stderr}</pre>
        {/if}
      </div>
    {/if}
  {/if}
</div>
