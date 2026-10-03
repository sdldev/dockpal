<script lang="ts">
  // Per-server security activity card (fail2ban + firewall monitoring) on the
  // server detail page. SSH on the backend is deliberately tight: the card
  // starts collapsed showing only the cached sec_fail2ban summary (zero SSH),
  // and expanding/refreshing hits GET /security/activity, which is TTL-cached
  // and single-flighted server-side. Auto-refresh is opt-in (60s).
  import { api } from '../../lib/api/client';
  import { addToast, isAdmin } from '../../lib/store';
  import type { SecurityActivity, SecurityActivityResponse } from '../../lib/types/generated';
  import Button from '../ui/Button.svelte';
  import ConfirmDialog from '../ui/ConfirmDialog.svelte';
  import Icon from '../ui/Icon.svelte';

  interface Props {
    instanceId: string;
    // Cached fail2ban state from the instance record (the Servers-table
    // badge value) — shown collapsed so a glance costs no SSH connection.
    fail2banState?: string;
  }
  let { instanceId, fail2banState = '' }: Props = $props();

  let expanded = $state(false);
  let activity = $state<SecurityActivity | null>(null);
  let error = $state('');
  let loading = $state(false);
  let cached = $state(false);
  let autoRefresh = $state(false);

  // Unban flow: row button → ConfirmDialog → POST → toast → refetch.
  let unbanTarget = $state<string | null>(null);
  let unbanning = $state(false);

  const isLocal = $derived(instanceId === 'local');

  // Reset everything when the viewed server changes (deep links, switcher).
  $effect(() => {
    void instanceId;
    activity = null;
    error = '';
    cached = false;
    autoRefresh = false;
  });

  // On-demand fetch: expanding with no data in hand is the only SSH trigger.
  $effect(() => {
    if (expanded && !activity && !loading && !isLocal) {
      fetchActivity();
    }
  });

  // Opt-in auto-refresh, alive only while the card stays open. Each tick
  // forces a fresh fetch — it fires at the TTL boundary anyway.
  $effect(() => {
    if (!expanded || !autoRefresh || isLocal) return;
    const t = setInterval(() => fetchActivity(true), 60000);
    return () => clearInterval(t);
  });

  async function fetchActivity(force = false) {
    loading = true;
    error = '';
    try {
      const resp = await api.get<SecurityActivityResponse>(
        `/instances/${encodeURIComponent(instanceId)}/security/activity${force ? '?force=true' : ''}`
      );
      activity = resp.activity;
      cached = resp.cached;
      error = resp.error ?? '';
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load security activity';
    } finally {
      loading = false;
    }
  }

  async function confirmUnban() {
    if (!unbanTarget) return;
    unbanning = true;
    try {
      const resp = await api.post<{ message: string }>(
        `/instances/${encodeURIComponent(instanceId)}/security/unban`,
        { ip: unbanTarget }
      );
      addToast(resp.message || `${unbanTarget} unbanned`, 'success');
      unbanTarget = null;
      await fetchActivity();
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Unban failed', 'error');
    } finally {
      unbanning = false;
    }
  }

  const f2bSummary = $derived(activity ? activity.fail2ban.active : fail2banState || 'unknown');
  const f2bChip = $derived.by(() => {
    switch (f2bSummary) {
      case 'active':
        return { text: 'fail2ban active', class: 'text-emerald-400 bg-emerald-500/10 border-emerald-500/30' };
      case 'inactive':
        return { text: 'fail2ban off', class: 'text-amber-300 bg-amber-500/10 border-amber-500/30' };
      default:
        return { text: 'fail2ban unknown', class: 'text-zinc-400 bg-zinc-800 border-zinc-700' };
    }
  });

  function firewallChip(act: SecurityActivity | null) {
    if (!act) return { text: 'firewall —', class: 'text-zinc-400 bg-zinc-800 border-zinc-700' };
    if (act.firewall.tool === 'none')
      return { text: 'no firewall found', class: 'text-zinc-400 bg-zinc-800 border-zinc-700' };
    const label = act.firewall.tool === 'unknown' ? 'firewall' : act.firewall.tool;
    if (act.firewall.active === 'active')
      return { text: `${label} active`, class: 'text-emerald-400 bg-emerald-500/10 border-emerald-500/30' };
    if (act.firewall.active === 'inactive')
      return { text: `${label} inactive`, class: 'text-amber-300 bg-amber-500/10 border-amber-500/30' };
    return { text: `${label} unknown`, class: 'text-zinc-400 bg-zinc-800 border-zinc-700' };
  }

  function chipClass(state: string): string {
    switch (state) {
      case 'active':
        return 'text-emerald-400 bg-emerald-500/10 border-emerald-500/30';
      case 'inactive':
        return 'text-amber-300 bg-amber-500/10 border-amber-500/30';
      default:
        return 'text-zinc-400 bg-zinc-800 border-zinc-700';
    }
  }

  function checkedAtLabel(ts: number): string {
    if (!ts) return '';
    return new Date(ts * 1000).toLocaleTimeString();
  }
</script>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm">
  {#if isLocal}
    <div class="flex items-center gap-2 px-4 py-3">
      <Icon name="admin" class="w-4 h-4 text-zinc-500" />
      <span class="text-sm text-zinc-500">
        Security monitoring is available for servers managed over SSH.
      </span>
    </div>
  {:else}
    <button
      type="button"
      class="w-full flex items-center justify-between gap-3 px-4 py-3 text-left hover:bg-zinc-800/40 transition-colors"
      aria-expanded={expanded}
      onclick={() => (expanded = !expanded)}
    >
      <span class="flex items-center gap-2 min-w-0">
        <Icon name="admin" class="w-4 h-4 text-zinc-400 shrink-0" />
        <span class="text-sm font-medium text-white">Security activity</span>
        <span class="hidden sm:inline-flex items-center gap-1.5">
          <span class="px-2 py-0.5 text-xs rounded-sm border {f2bChip.class}">{f2bChip.text}</span>
          {#if activity && activity.fail2ban.active === 'active'}
            <span class="px-2 py-0.5 text-xs rounded-sm border {chipClass('active')}">
              {activity.fail2ban.currently_banned} banned
            </span>
          {/if}
          <span class="px-2 py-0.5 text-xs rounded-sm border {firewallChip(activity).class}">
            {firewallChip(activity).text}
          </span>
        </span>
      </span>
      <Icon
        name="chevron-down"
        class="w-4 h-4 text-zinc-500 transition-transform {expanded ? 'rotate-180' : ''} shrink-0"
      />
    </button>

    {#if expanded}
      <div class="border-t border-zinc-800 p-4 space-y-5">
        <div class="flex items-center justify-between gap-2 flex-wrap">
          <div class="flex items-center gap-2">
            {#if loading}
              <span class="text-xs text-zinc-500">Checking over SSH…</span>
            {:else if activity}
              <span class="text-xs text-zinc-500">
                Checked {checkedAtLabel(activity.checked_at)}
                {#if cached}<span class="ml-1 px-1.5 py-0.5 rounded-sm bg-zinc-800 text-zinc-400">cached</span>{/if}
              </span>
            {/if}
          </div>
          <div class="flex items-center gap-3">
            <label class="flex items-center gap-1.5 text-xs text-zinc-400 cursor-pointer select-none">
              <input type="checkbox" bind:checked={autoRefresh} class="accent-violet-500" />
              Auto 60s
            </label>
            <Button variant="secondary" size="sm" loading={loading} onclick={() => fetchActivity(true)}>
              Refresh
            </Button>
          </div>
        </div>

        {#if error}
          <div class="px-3 py-2 text-xs text-amber-300 bg-amber-500/10 border border-amber-500/30 rounded-sm">
            {error}
          </div>
        {/if}

        {#if activity}
          <!-- fail2ban -->
          <div class="space-y-3">
            <div class="flex items-center gap-2">
              <h4 class="text-sm font-medium text-zinc-300">fail2ban (sshd jail)</h4>
              <span class="px-2 py-0.5 text-xs rounded-sm border {chipClass(activity.fail2ban.active)}">
                {activity.fail2ban.active}
              </span>
            </div>

            {#if activity.fail2ban.read_error}
              <p class="text-xs text-amber-300">{activity.fail2ban.read_error}</p>
            {:else if activity.fail2ban.active === 'active'}
              <div class="grid grid-cols-3 gap-2 max-w-md">
                <div class="bg-zinc-950 border border-zinc-800 rounded-sm px-3 py-2">
                  <p class="text-xs text-zinc-500">Currently banned</p>
                  <p class="text-lg font-semibold text-white">{activity.fail2ban.currently_banned}</p>
                </div>
                <div class="bg-zinc-950 border border-zinc-800 rounded-sm px-3 py-2">
                  <p class="text-xs text-zinc-500">Total banned</p>
                  <p class="text-lg font-semibold text-white">{activity.fail2ban.total_banned}</p>
                </div>
                <div class="bg-zinc-950 border border-zinc-800 rounded-sm px-3 py-2">
                  <p class="text-xs text-zinc-500">Total failed</p>
                  <p class="text-lg font-semibold text-white">{activity.fail2ban.total_failed}</p>
                </div>
              </div>

              {#if activity.fail2ban.banned_ips.length > 0}
                <div>
                  <p class="text-xs text-zinc-500 mb-1.5">Banned IP list</p>
                  <ul role="list" class="space-y-1">
                    {#each activity.fail2ban.banned_ips as ip (ip)}
                      <li class="flex items-center justify-between gap-3 px-2.5 py-1.5 rounded-sm bg-zinc-950 border border-zinc-800">
                        <span class="font-mono text-sm text-red-300">{ip}</span>
                        {#if $isAdmin}
                          <Button variant="secondary" size="sm" onclick={() => (unbanTarget = ip)}>
                            Unban
                          </Button>
                        {/if}
                      </li>
                    {/each}
                  </ul>
                </div>
              {:else}
                <p class="text-sm text-zinc-600">No IPs currently banned.</p>
              {/if}

              {#if activity.fail2ban.events.length > 0}
                <details>
                  <summary class="text-xs text-zinc-500 cursor-pointer hover:text-zinc-300 select-none">
                    Recent fail2ban events ({activity.fail2ban.events.length})
                  </summary>
                  <pre class="mt-1.5 font-mono text-xs text-zinc-400 bg-zinc-950 border border-zinc-800 rounded-sm p-3 overflow-auto max-h-44 whitespace-pre-wrap">{activity.fail2ban.events.join('\n')}</pre>
                </details>
              {/if}
            {:else if activity.fail2ban.active === 'inactive'}
              <p class="text-sm text-zinc-600">
                fail2ban is not running on this server — enable it from the SSH access &amp;
                hardening controls above.
              </p>
            {/if}
          </div>

          <!-- firewall -->
          <div class="space-y-3">
            <div class="flex items-center gap-2">
              <h4 class="text-sm font-medium text-zinc-300">Firewall</h4>
              {#if activity.firewall.tool !== 'none' && activity.firewall.tool !== 'unknown'}
                <span class="px-2 py-0.5 text-xs rounded-sm border {chipClass(activity.firewall.active)}">
                  {activity.firewall.tool} {activity.firewall.active}
                </span>
              {/if}
            </div>

            {#if activity.firewall.read_error}
              <p class="text-xs text-amber-300">{activity.firewall.read_error}</p>
            {:else if activity.firewall.tool === 'none'}
              <p class="text-sm text-zinc-600">
                No ufw/firewalld installation detected on this server.
              </p>
            {:else if activity.firewall.tool === 'ufw'}
              <p class="text-sm text-zinc-400">
                {activity.firewall.blocks_24h}
                blocked packet{activity.firewall.blocks_24h === 1 ? '' : 's'} in the last 24h
                {#if activity.firewall.blocks_24h >= 200}(count capped){/if}
              </p>
              {#if activity.firewall.status}
                <details>
                  <summary class="text-xs text-zinc-500 cursor-pointer hover:text-zinc-300 select-none">
                    ufw rules
                  </summary>
                  <pre class="mt-1.5 font-mono text-xs text-zinc-400 bg-zinc-950 border border-zinc-800 rounded-sm p-3 overflow-auto max-h-44">{activity.firewall.status}</pre>
                </details>
              {/if}
              {#if activity.firewall.block_lines.length > 0}
                <details>
                  <summary class="text-xs text-zinc-500 cursor-pointer hover:text-zinc-300 select-none">
                    Recent blocked packets ({activity.firewall.block_lines.length})
                  </summary>
                  <pre class="mt-1.5 font-mono text-xs text-zinc-400 bg-zinc-950 border border-zinc-800 rounded-sm p-3 overflow-auto max-h-44 whitespace-pre-wrap">{activity.firewall.block_lines.join('\n')}</pre>
                </details>
              {/if}
            {:else if activity.firewall.tool === 'firewalld'}
              <p class="text-sm text-zinc-400">firewalld state: {activity.firewall.status || 'unknown'}</p>
            {:else}
              <p class="text-sm text-zinc-600">Firewall state could not be determined.</p>
            {/if}
          </div>
        {:else if !error}
          <p class="text-sm text-zinc-600">No data yet.</p>
        {/if}
      </div>
    {/if}
  {/if}
</div>

<ConfirmDialog
  open={unbanTarget !== null}
  title="Unban IP"
  message="Lift the fail2ban ban for {unbanTarget ?? ''} on this server? The address will be able to connect again immediately."
  confirmLabel="Unban"
  busy={unbanning}
  onconfirm={confirmUnban}
  onclose={() => (unbanTarget = null)}
/>
