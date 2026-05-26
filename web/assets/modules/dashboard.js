// Dashboard page: system resources, polling.
window.Dockpal = window.Dockpal || {};

Dockpal.dashboard = {
  async loadDashboard() {
    // Use instanceApi for instance-scoped container and system info requests
    const resp = await this.instanceApi('GET', '/containers');
    if (!resp) return;
    this.containers = await resp.json();

    if (this.sysResourceHistory.labels.length === 0) {
      const sysResp = await this.instanceApi('GET', '/system/info');
      if (sysResp) this.systemInfo = await sysResp.json();
      // Pre-fill chart with synthetic baseline so the line is visible immediately
      const baseCpu = this.systemInfo?.cpu_percent || 0;
      const baseRam = this.systemInfo?.total_ram > 0
        ? (this.systemInfo.used_ram / this.systemInfo.total_ram * 100) : 0;
      for (let i = 19; i >= 0; i--) {
        const t = new Date(Date.now() - i * 2500);
        this.sysResourceHistory.labels.push(t.toLocaleTimeString());
        this.sysResourceHistory.cpu.push(baseCpu + (Math.random() * 2 - 1));
        this.sysResourceHistory.ram.push(baseRam + (Math.random() * 0.3 - 0.15));
      }
      this.renderSysResourceChart();
    }
    this.startSysResourcePolling();
  },

  async fetchSystemInfo() {
    // Use instanceApi for instance-scoped system info
    const sysResp = await this.instanceApi('GET', '/system/info');
    if (sysResp) {
      this.systemInfo = await sysResp.json();
      const now = new Date().toLocaleTimeString();
      this.sysResourceHistory.labels.push(now);
      this.sysResourceHistory.cpu.push(this.systemInfo.cpu_percent || 0);
      this.sysResourceHistory.ram.push(this.systemInfo.total_ram > 0 ? (this.systemInfo.used_ram / this.systemInfo.total_ram * 100) : 0);
      if (this.sysResourceHistory.labels.length > 30) {
        this.sysResourceHistory.labels.shift();
        this.sysResourceHistory.cpu.shift();
        this.sysResourceHistory.ram.shift();
      }
      this.renderSysResourceChart();
    }
  },

  startSysResourcePolling() {
    if (this.sysResourceInterval) clearInterval(this.sysResourceInterval);
    this.sysResourceInterval = setInterval(() => {
      if (this.currentPage === 'dashboard') this.fetchSystemInfo();
    }, 2500);
  },

  async loadPageData(page) {
    if (page === 'dashboard') await this.loadDashboard();
    else if (page === 'fleet') await this.loadFleet();
    else if (page === 'templates') await this.loadTemplates();
    else if (page === 'images') await this.loadImages();
    else if (page === 'containers') await this.loadDashboard();
    else if (page === 'apps') await this.loadApps();
    else if (page === 'deploy') { await this.loadServices(); this.loadGithubRepos(); }
    else if (page === 'domains') await this.loadDomains();
    else if (page === 'registry') await this.loadRegistries();
    else if (page === 'profile') await this.loadProfile();
  },
};
