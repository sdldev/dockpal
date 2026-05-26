// Containers: list, filter, actions, detail page, stats polling, log streaming.
window.Dockpal = window.Dockpal || {};

Dockpal.containers = {
  dedupePorts(ports) {
    if (!Array.isArray(ports)) return [];
    const seen = new Set();
    const out = [];
    for (const p of ports) {
      const key = (p.PublicPort || 0) + ':' + p.PrivatePort + '/' + p.Type;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push(p);
    }
    return out;
  },

  isProtectedContainer(c) {
    if (!c) return false;
    if (c.protected === true) return true;

    const name = (c.name || '').replace(/^\//, '').toLowerCase();
    const image = (c.image || '').toLowerCase();
    const env = Array.isArray(c.env) ? c.env : [];
    const hasAgentEnv = env.some(e =>
      e.startsWith('DOCKPAL_MODE=') ||
      e.startsWith('DOCKPAL_TOKEN=') ||
      e.startsWith('DOCKPAL_SERVER=') ||
      e.startsWith('DOCKPAL_EDGE_SERVER=')
    );

    return name === 'dockpal-agent' && (image.includes('sdldev/dockpal-agent') || hasAgentEnv);
  },

  async selectContainer(c) {
    this.cleanupContainerDetail();
    // Use instanceApi for container operations
    const resp = await this.instanceApi('GET', '/containers/' + c.id);
    if (!resp || !resp.ok) return;
    this.selectedContainer = await resp.json();
    this.logs = [];
    this.statsHistory = { cpu: [], mem: [], rx: [], tx: [], labels: [] };
    this.containerStats = null;
    this.containerDetailTab = 'overview';
    this.containerEditMode = false;
    this.containerEditSaving = false;
    this.containerImageUpdateResult = null;
    this.currentPage = 'container-detail';
    this.startStatsPolling(this.selectedContainer.id);
    this.startLogStream(this.selectedContainer.id);
  },

  async refreshContainerDetail() {
    if (!this.selectedContainer) return;
    setTimeout(async () => {
      const lookup = this.selectedContainer.name || this.selectedContainer.id;
      // Use instanceApi for container inspect
      const resp = await this.instanceApi('GET', '/containers/' + lookup);
      if (resp && resp.ok) this.selectedContainer = await resp.json();
    }, 500);
  },

  async refreshContainerDetailNow(id) {
    const lookup = id || this.selectedContainer?.id || this.selectedContainer?.name;
    if (!lookup) return;
    const resp = await this.instanceApi('GET', '/containers/' + lookup);
    if (resp && resp.ok) this.selectedContainer = await resp.json();
  },

  containerAppName(c) {
    const labels = c?.labels || this.containers.find(x => x.id === c?.id || x.name === c?.name)?.labels || {};
    return labels['dockpal.project'] || labels['com.docker.compose.project'] || '';
  },

  isManagedAppContainer(c) {
    return !!this.containerAppName(c);
  },

  selectedContainerUpdateAvailable() {
    const imageRef = this.selectedContainer?.image;
    return !!(
      this.containerImageUpdateResult?.has_update ||
      this.getImageUpdateStatus(imageRef)?.result?.has_update
    );
  },

  restartPolicyLabel(c) {
    const policy = c?.restart_policy || 'no';
    return policy === '' ? 'no' : policy;
  },

  isRestartPolicyUnsafe(c) {
    const policy = this.restartPolicyLabel(c);
    return policy === 'no' || policy === 'on-failure';
  },

  containersWithoutAutoStart() {
    return (this.containers || []).filter(c => !this.isProtectedContainer(c) && this.isRestartPolicyUnsafe(c));
  },

  async setContainerRestartPolicy(container, policy = 'unless-stopped') {
    if (!container?.id) return;
    if (this.isProtectedContainer(container)) {
      this.toast(container.protection_reason || 'Protected container cannot be changed here', 'warning', 5000);
      return;
    }
    const resp = await this.instanceApi('PUT', '/containers/' + container.id, { restart_policy: policy });
    if (!resp || !resp.ok) {
      const data = resp ? await resp.json().catch(() => ({})) : {};
      this.toast(data.error || 'Failed to update restart policy', 'error', 5000);
      return;
    }
    const result = await resp.json();
    this.toast('Restart policy set to ' + policy, 'success');
    if (result.container) {
      if (this.selectedContainer?.id === container.id || this.selectedContainer?.name === container.name) {
        this.selectedContainer = result.container;
      }
    }
    await this.loadDashboard();
  },

  setAllUnsafeRestartPolicies() {
    const targets = this.containersWithoutAutoStart();
    if (targets.length === 0) {
      this.toast('All visible containers already have reboot-safe restart policies', 'success');
      return;
    }
    this.showConfirm({
      title: 'Enable auto-start after reboot',
      message: 'Set restart policy to unless-stopped for ' + targets.length + ' container(s). Containers manually stopped later will remain stopped after reboot.',
      confirmText: 'Set unless-stopped',
      danger: false,
      onConfirm: async () => {
        let ok = 0;
        for (const c of targets) {
          const resp = await this.instanceApi('PUT', '/containers/' + c.id, { restart_policy: 'unless-stopped' });
          if (resp && resp.ok) ok++;
        }
        this.toast('Updated restart policy for ' + ok + '/' + targets.length + ' container(s)', ok === targets.length ? 'success' : 'warning', 6000);
        await this.loadDashboard();
        if (this.selectedContainer) await this.refreshContainerDetailNow(this.selectedContainer.id || this.selectedContainer.name);
      }
    });
  },

  startStatsPolling(id) {
    if (this.statsInterval) clearInterval(this.statsInterval);
    this.statsHistory = { cpu: [], mem: [], rx: [], tx: [], labels: [] };
    const fetchStats = async () => {
      // Use instanceApi for container stats
      const resp = await this.instanceApi('GET', '/containers/' + id + '/stats');
      if (!resp) return;
      this.containerStats = await resp.json();
      const now = new Date().toLocaleTimeString();
      this.statsHistory.labels.push(now);
      this.statsHistory.cpu.push(this.containerStats.cpu_percent || 0);
      this.statsHistory.mem.push(this.containerStats.memory_percent || 0);
      this.statsHistory.rx.push(this.containerStats.network_rx || 0);
      this.statsHistory.tx.push(this.containerStats.network_tx || 0);
      if (this.statsHistory.labels.length > 30) {
        ['labels', 'cpu', 'mem', 'rx', 'tx'].forEach(k => this.statsHistory[k].shift());
      }
      this.renderContainerCharts();
    };
    fetchStats();
    this.statsInterval = setInterval(fetchStats, 2500);
  },

  async startLogStream(id) {
    try {
      this.closeContainerLogStream();
      const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      // Use instance-scoped WebSocket path (Requirement 12.8)
      const wsUrl = wsProto + '//' + location.host + '/api/instances/' + this.selectedInstance + '/containers/' + id + '/logs?token=' + this.token;
      const ws = new WebSocket(wsUrl);
      this.containerLogSocket = ws;
      ws.onmessage = (e) => {
        const lines = e.data.split('\n').filter(l => l.trim());
        this.logs = [...this.logs.slice(-200), ...lines];
      };
      ws.onerror = () => {};
      ws.onclose = () => {
        if (this.containerLogSocket === ws) this.containerLogSocket = null;
      };
    } catch (e) {}
  },

  async containerAction(id, action) {
    const labels = { start: 'started', stop: 'stopped', restart: 'restarted', remove: 'removed' };
    if (action === 'remove') {
      const container = this.selectedContainer?.id === id ? this.selectedContainer : this.containers.find(c => c.id === id || c.name === id);
      if (this.isProtectedContainer(container)) {
        this.toast(container.protection_reason || 'Dockpal agent container cannot be removed from Dockpal', 'warning', 5000);
        return;
      }
      this.showConfirm({
        title: 'Remove Container',
        message: 'This will stop and permanently remove the container. Volumes and data may be lost.',
        confirmText: 'Remove',
        onConfirm: async () => {
          // Use instanceApi for container deletion
          const resp = await this.instanceApi('DELETE', '/containers/' + id + '?force=true');
          if (resp && !resp.ok) {
            const data = await resp.json().catch(() => ({}));
            this.toast(data.error || 'Failed to remove container', 'error', 5000);
          } else {
            this.toast('Container removed', 'success');
            // Redirect to containers list after successful removal
            this.currentPage = 'containers';
            this.selectedContainer = null;
            this.destroyChart();
          }
          await this.loadDashboard();
        }
      });
      return;
    }
    // Use instanceApi for container actions
    if (action === 'restart') {
      this.toast('Restarting container. Restart does not pull latest images; use Pull latest & recreate to apply image updates.', 'info', 6000);
    }
    const resp = await this.instanceApi('POST', '/containers/' + id + '/' + action);
    if (resp && !resp.ok) {
      const data = await resp.json().catch(() => ({}));
      this.toast(data.error || ('Failed to ' + action), 'error', 5000);
    } else {
      const suffix = action === 'restart' ? ' (same image)' : '';
      this.toast('Container ' + (labels[action] || action) + suffix, 'success');
    }
    await this.loadDashboard();
  },

  // Generate memory limit dropdown options capped at host total RAM.
  // Returns [{value, label}] with recommended values in MB.
  memoryLimitOptions() {
    const totalMB = Math.floor((this.systemInfo?.total_ram || 0) / (1024 * 1024));
    if (totalMB === 0) return [];
    // Standard memory tiers (MB)
    const tiers = [128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536];
    const opts = [];
    for (const mb of tiers) {
      if (mb >= totalMB) break;
      const label = mb >= 1024 ? (mb / 1024) + ' GB' : mb + ' MB';
      const recommended = mb === 512 || mb === 1024;
      opts.push({ value: mb, label: label + (recommended ? ' ★' : '') });
    }
    // Add host max as final option
    if (totalMB > 0) {
      const label = totalMB >= 1024 ? (totalMB / 1024).toFixed(0) + ' GB' : totalMB + ' MB';
      opts.push({ value: totalMB, label: label + ' (host max)' });
    }
    return opts;
  },

  // Generate CPU limit dropdown options capped at host CPU cores.
  // Returns [{value, label}] with recommended values.
  cpuLimitOptions() {
    const maxCores = this.systemInfo?.cpu_cores || 0;
    if (maxCores === 0) return [];
    const opts = [];
    // Offer 0.5 increments up to host max
    for (let c = 0.5; c <= maxCores; c += 0.5) {
      const label = (c % 1 === 0 ? c.toFixed(0) : c.toFixed(1)) + ' core' + (c > 1 ? 's' : '');
      const recommended = c === 1 || c === 2;
      opts.push({ value: c, label: label + (recommended ? ' ★' : '') });
    }
    return opts;
  },

  async enterContainerEditMode() {
    const c = this.selectedContainer;
    if (!c) return;
    // Ensure systemInfo is loaded for memory/CPU dropdown limits - use instanceApi
    if (!this.systemInfo) {
      try {
        const resp = await this.instanceApi('GET', '/system/info');
        if (resp && resp.ok) this.systemInfo = await resp.json();
      } catch (e) {}
    }
    // Parse current env vars into key/value pairs
    const envPairs = (c.env || []).map(e => {
      const idx = e.indexOf('=');
      return idx >= 0 ? { key: e.substring(0, idx), value: e.substring(idx + 1) } : { key: e, value: '' };
    });
    // Parse current ports from PortSummary format
    const ports = this.dedupePorts(c.ports || []).map(p => ({
      host_port: p.PublicPort || '',
      container_port: p.PrivatePort || '',
      protocol: p.Type || 'tcp'
    }));
    // Parse current volumes from mounts
    const volumes = (c.mounts || []).filter(m => m.Source && m.Destination).map(m => ({
      host_path: m.Source,
      container_path: m.Destination,
      read_only: m.RW === false
    }));

    this.editContainerForm = {
      name: c.name || '',
      image: c.image || '',
      restart_policy: c.restart_policy || '',
      memory_mb: c.memory_limit ? Math.round(c.memory_limit / (1024 * 1024)) : 0,
      cpu_limit: c.nano_cpus ? (c.nano_cpus / 1e9) : 0,
      env: envPairs,
      ports: ports,
      volumes: volumes
    };
    this.containerEditMode = true;
    this.containerEditSaving = false;
  },

  cancelContainerEdit() {
    this.containerEditMode = false;
  },

  async submitContainerEdit() {
    const form = this.editContainerForm;
    const c = this.selectedContainer;
    const body = {};

    if (form.name && form.name !== (c?.name || '')) body.name = form.name;
    if (form.image && form.image !== (c?.image || '')) body.image = form.image;
    if (form.restart_policy && form.restart_policy !== (c?.restart_policy || '')) body.restart_policy = form.restart_policy;

    // Memory limit: always send if changed from current value
    const memMB = Number(form.memory_mb) || 0;
    const currentMemMB = c?.memory_limit ? Math.round(c.memory_limit / (1024 * 1024)) : 0;
    if (memMB !== currentMemMB) {
      body.memory_limit = memMB * 1024 * 1024; // 0 = unlimited
    }

    // CPU limit: always send if changed from current value
    const cpuLim = Number(form.cpu_limit) || 0;
    const currentCpu = c?.nano_cpus ? (c.nano_cpus / 1e9) : 0;
    if (cpuLim !== currentCpu) {
      body.cpu_limit = cpuLim; // 0 = unlimited
    }

    const validEnv = form.env.filter(e => e.key.trim() !== '');
    if (validEnv.length > 0) body.env = validEnv.map(e => e.key + '=' + e.value);

    const validPorts = form.ports.filter(p => p.host_port && p.container_port);
    if (validPorts.length > 0) body.ports = validPorts.map(p => ({
      host_port: Number(p.host_port),
      container_port: Number(p.container_port),
      protocol: p.protocol || 'tcp'
    }));

    const validVolumes = form.volumes.filter(v => v.host_path.trim() && v.container_path.trim());
    if (validVolumes.length > 0) body.volumes = validVolumes.map(v => ({
      host_path: v.host_path,
      container_path: v.container_path,
      read_only: v.read_only || false
    }));

    if (Object.keys(body).length === 0) {
      this.toast('No changes to apply', 'info');
      this.containerEditMode = false;
      return;
    }

    this.containerEditSaving = true;
    // Use instanceApi for container edit
    const resp = await this.instanceApi('PUT', '/containers/' + (c.name || c.id), body);
    if (!resp || !resp.ok) {
      this.containerEditSaving = false;
      const data = await resp?.json?.().catch(() => ({}));
      this.toast(data.error || 'Failed to update container', 'error', 5000);
      return;
    }

    const result = await resp.json();
    this.containerEditSaving = false;
    this.containerEditMode = false;

    if (result.recreated) {
      this.toast('Container recreated with new config', 'success');
      await this.loadDashboard();
      if (result.container) {
        this.selectedContainer = result.container;
        this.destroyChart();
        this.startStatsPolling(result.container.id);
        this.startLogStream(result.container.id);
      }
    } else {
      this.toast('Container updated successfully', 'success');
      if (result.container) {
        this.selectedContainer = result.container;
      }
      await this.refreshContainerDetail();
    }
  },
};
