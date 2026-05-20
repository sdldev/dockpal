// Fleet Dashboard module: multi-instance monitoring & bulk deployment.
window.Dockpal = window.Dockpal || {};

Dockpal.fleet = {
  async loadFleet() {
    this.fleetContainers = [];
    this.fleetInstances = [];
    
    // Load instance list first
    await this.loadInstances();
    
    // Perform initial fetch of metrics
    await this.fetchFleetMetrics();
    
    // Start polling
    this.startFleetPolling();
  },

  async fetchFleetMetrics() {
    const list = this.instances || [];
    
    const promises = list.map(async (inst) => {
      let sysInfo = null;
      let containers = [];
      const isOnline = inst.id === 'local' || inst.status === 'online';

      if (isOnline) {
        try {
          const sysResp = await this.api('GET', `/api/instances/${inst.id}/system/info`);
          if (sysResp && sysResp.ok) {
            sysInfo = await sysResp.json();
          }
        } catch (e) {
          console.error(`Failed to get system info for instance ${inst.id}:`, e);
        }

        try {
          const containersResp = await this.api('GET', `/api/instances/${inst.id}/containers`);
          if (containersResp && containersResp.ok) {
            containers = await containersResp.json();
          }
        } catch (e) {
          console.error(`Failed to get containers for instance ${inst.id}:`, e);
        }
      }

      return {
        ...inst,
        sysInfo,
        containers
      };
    });

    const resolved = await Promise.all(promises);
    this.fleetInstances = resolved;

    // Flatten all containers for global view
    const allContainers = [];
    resolved.forEach((inst) => {
      if (inst.containers && Array.isArray(inst.containers)) {
        inst.containers.forEach((c) => {
          allContainers.push({
            ...c,
            instanceId: inst.id,
            instanceName: inst.id === 'local' ? 'This Server' : inst.name
          });
        });
      }
    });
    this.fleetContainers = allContainers;
  },

  startFleetPolling() {
    if (this.fleetInterval) clearInterval(this.fleetInterval);
    this.fleetInterval = setInterval(() => {
      if (this.currentPage === 'fleet') {
        this.fetchFleetMetrics();
      } else {
        clearInterval(this.fleetInterval);
        this.fleetInterval = null;
      }
    }, 5000);
  },

  async executeBulkDeploy() {
    this.bulkDeploying = true;
    this.bulkDeployLogs = [];

    const targets = this.bulkDeployForm.targets || [];
    if (targets.length === 0) {
      this.toast('Please select at least one deployment target', 'error');
      this.bulkDeploying = false;
      return;
    }

    const name = this.bulkDeployForm.name;
    const compose = this.bulkDeployForm.compose;
    const timestampStr = () => new Date().toLocaleTimeString();

    this.addBulkDeployLog(timestampStr(), 'info', `Starting bulk deployment of "${name}" to ${targets.length} targets...`, 'running');

    // Run deployments sequentially or concurrently. Concurrently is better.
    const promises = targets.map(async (targetId) => {
      const inst = this.instances.find(i => i.id === targetId);
      const displayName = inst ? (inst.id === 'local' ? 'This Server' : inst.name) : targetId;
      
      this.addBulkDeployLog(timestampStr(), 'info', `Deploying to ${displayName}...`, 'running');

      try {
        const resp = await this.api('POST', `/api/instances/${targetId}/deploy/compose`, {
          name,
          compose
        });

        if (resp && resp.ok) {
          this.addBulkDeployLog(timestampStr(), 'success', `Deployment to ${displayName} succeeded.`, 'success');
          return { id: targetId, status: 'success' };
        } else {
          const data = await resp.json().catch(() => ({}));
          const errMsg = data.error || 'Unknown error';
          this.addBulkDeployLog(timestampStr(), 'error', `Deployment to ${displayName} failed: ${errMsg}`, 'failed');
          return { id: targetId, status: 'failed' };
        }
      } catch (e) {
        this.addBulkDeployLog(timestampStr(), 'error', `Deployment to ${displayName} failed: Connection error`, 'failed');
        return { id: targetId, status: 'failed' };
      }
    });

    const results = await Promise.all(promises);
    const succeeded = results.filter(r => r.status === 'success').length;
    const failed = results.filter(r => r.status === 'failed').length;

    this.addBulkDeployLog(timestampStr(), 'info', `Bulk deployment completed: ${succeeded} succeeded, ${failed} failed.`, succeeded > 0 ? 'success' : 'failed');
    this.toast(`Bulk deployment done: ${succeeded} succeeded, ${failed} failed`, succeeded > 0 ? 'success' : 'error');

    this.bulkDeploying = false;
    
    // Reset form targets and name/compose
    this.bulkDeployForm.name = '';
    this.bulkDeployForm.compose = '';
    this.bulkDeployForm.targets = [];

    // Reload fleet info
    await this.fetchFleetMetrics();
  },

  addBulkDeployLog(timestamp, type, message, status) {
    this.bulkDeployLogs.push({
      timestamp,
      type,
      message,
      status
    });
  }
};
