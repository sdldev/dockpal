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

  async selectContainer(c) {
    const resp = await this.api('GET', '/api/containers/' + c.id);
    if (!resp) return;
    this.selectedContainer = await resp.json();
    this.logs = [];
    this.statsHistory = { cpu: [], mem: [], rx: [], tx: [], labels: [] };
    this.containerStats = null;
    this.containerDetailTab = 'overview';
    this.currentPage = 'container-detail';
    this.destroyChart();
    this.startStatsPolling(c.id);
    this.startLogStream(c.id);
  },

  async refreshContainerDetail() {
    if (!this.selectedContainer) return;
    setTimeout(async () => {
      const resp = await this.api('GET', '/api/containers/' + this.selectedContainer.id);
      if (resp && resp.ok) this.selectedContainer = await resp.json();
    }, 500);
  },

  startStatsPolling(id) {
    if (this.statsInterval) clearInterval(this.statsInterval);
    this.statsHistory = { cpu: [], mem: [], rx: [], tx: [], labels: [] };
    const fetchStats = async () => {
      const resp = await this.api('GET', '/api/containers/' + id + '/stats');
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
      const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const ws = new WebSocket(wsProto + '//' + location.host + '/api/containers/' + id + '/logs');
      ws.onmessage = (e) => {
        const lines = e.data.split('\n').filter(l => l.trim());
        this.logs = [...this.logs.slice(-200), ...lines];
      };
      ws.onerror = () => {};
    } catch (e) {}
  },

  async containerAction(id, action) {
    const labels = { start: 'started', stop: 'stopped', restart: 'restarted', remove: 'removed' };
    if (action === 'remove') {
      this.showConfirm({
        title: 'Remove Container',
        message: 'This will stop and permanently remove the container. Volumes and data may be lost.',
        confirmText: 'Remove',
        onConfirm: async () => {
          const resp = await this.api('DELETE', '/api/containers/' + id + '?force=true');
          if (resp && !resp.ok) {
            const data = await resp.json().catch(() => ({}));
            this.toast(data.error || 'Failed to remove container', 'error', 5000);
          } else {
            this.toast('Container removed', 'success');
          }
          await this.loadDashboard();
        }
      });
      return;
    }
    const resp = await this.api('POST', '/api/containers/' + id + '/' + action);
    if (resp && !resp.ok) {
      const data = await resp.json().catch(() => ({}));
      this.toast(data.error || ('Failed to ' + action), 'error', 5000);
    } else {
      this.toast('Container ' + (labels[action] || action), 'success');
    }
    await this.loadDashboard();
  },

  async submitContainerEdit() {
    const form = this.editContainerForm;
    const body = {};

    // Only include fields that the user actually changed
    if (form.name && form.name !== (this.selectedContainer?.name || '')) {
      body.name = form.name;
    }
    if (form.image && form.image !== (this.selectedContainer?.image || '')) {
      body.image = form.image;
    }
    if (form.restart_policy) {
      body.restart_policy = form.restart_policy;
    }
    if (form.memory_mb > 0) {
      body.memory_limit = form.memory_mb * 1024 * 1024; // MB → bytes
    }
    if (form.cpu_limit > 0) {
      body.cpu_limit = form.cpu_limit;
    }

    // Env: only send if user added entries
    const validEnv = form.env.filter(e => e.key.trim() !== '');
    if (validEnv.length > 0) {
      body.env = validEnv.map(e => e.key + '=' + e.value);
    }

    // Ports: only send if user added entries
    const validPorts = form.ports.filter(p => p.host_port && p.container_port);
    if (validPorts.length > 0) {
      body.ports = validPorts.map(p => ({
        host_port: Number(p.host_port),
        container_port: Number(p.container_port),
        protocol: p.protocol || 'tcp'
      }));
    }

    // Volumes: only send if user added entries
    const validVolumes = form.volumes.filter(v => v.host_path.trim() && v.container_path.trim());
    if (validVolumes.length > 0) {
      body.volumes = validVolumes.map(v => ({
        host_path: v.host_path,
        container_path: v.container_path,
        read_only: v.read_only || false
      }));
    }

    // Nothing to update?
    if (Object.keys(body).length === 0) {
      this.toast('No changes to apply', 'info');
      this.showEditContainerModal = false;
      return;
    }

    const resp = await this.api('PUT', '/api/containers/' + this.selectedContainer.id, body);
    if (!resp || !resp.ok) {
      const data = await resp?.json?.().catch(() => ({}));
      this.toast(data.error || 'Failed to update container', 'error', 5000);
      return;
    }

    const result = await resp.json();
    this.showEditContainerModal = false;

    if (result.recreated) {
      this.toast('Container recreated with new config', 'success');
      // After recreate, the container ID may have changed — refresh from list
      await this.loadDashboard();
      // Try to find the updated container by name
      if (result.container) {
        this.selectedContainer = result.container;
        // Restart stats/log polling with new ID
        this.destroyChart();
        this.startStatsPolling(result.container.id);
        this.startLogStream(result.container.id);
      }
    } else {
      this.toast('Container updated', 'success');
      this.selectedContainer = result.container || this.selectedContainer;
      await this.refreshContainerDetail();
    }
  },
};
