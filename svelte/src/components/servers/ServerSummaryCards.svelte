<script lang="ts">
	// Fleet-wide totals for the Servers overview. Reads the shared servers
	// store directly so the cards always match the table beside them.
	import { servers } from '$lib/store';
	import { formatBytes } from '$lib/format';

	const onlineInstances = $derived($servers.instances.filter((i) => servers.isOnline(i)));
	const runningCount = $derived($servers.containers.filter((c) => c.state === 'running').length);
	const totalCpuCores = $derived(
		$servers.instances.reduce((acc, inst) => acc + (inst.sysInfo?.cpu_cores || 0), 0)
	);
	const totalMemory = $derived(
		$servers.instances.reduce((acc, inst) => acc + (inst.sysInfo?.total_ram || 0), 0)
	);
	const onlineCount = $derived(onlineInstances.length);
	const offlineCount = $derived($servers.instances.length - onlineCount);
</script>

<div class="grid grid-cols-1 md:grid-cols-4 gap-4">
	<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
		<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total Instances</div>
		<div class="text-3xl font-bold text-white">{$servers.instances.length}</div>
		<div class="text-[10px] text-zinc-500 mt-1">{onlineCount} Online / {offlineCount} Offline</div>
	</div>
	<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
		<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total Running Containers</div>
		<div class="text-3xl font-bold text-emerald-400">{runningCount}</div>
		<div class="text-[10px] text-zinc-500 mt-1">Across all online agents</div>
	</div>
	<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
		<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total CPU Cores</div>
		<div class="text-3xl font-bold text-blue-400">{totalCpuCores}</div>
		<div class="text-[10px] text-zinc-500 mt-1">Total CPU capacity</div>
	</div>
	<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
		<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total Memory Capacity</div>
		<div class="text-3xl font-bold text-blue-400">{formatBytes(totalMemory)}</div>
		<div class="text-[10px] text-zinc-500 mt-1">Total RAM capacity</div>
	</div>
</div>
