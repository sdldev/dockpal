// Authentication, session lifecycle, and authenticated API wrapper.
window.Dockpal = window.Dockpal || {};

Dockpal.auth = {
  async init() {
    const saved = localStorage.getItem('dockpal_token');
    if (saved) {
      this.token = saved;
      try {
        // Load instances first to get the instance list
        await this.loadInstances();
        
        // Try to access the selected instance's containers
        const resp = await this.instanceApi('GET', '/containers');
        if (resp && resp.ok) {
          this.view = 'app';
          await this.loadDashboard();
          await this.checkForUpdates();
          return;
        }
        // If instance-scoped call fails, try local for backward compatibility
        const localResp = await fetch('/api/containers', { headers: { Authorization: 'Bearer ' + this.token } });
        if (localResp.ok) {
          // Reset to local instance if the selected instance is not accessible
          this.selectedInstance = 'local';
          this.view = 'app';
          await this.loadDashboard();
          await this.checkForUpdates();
          return;
        }
      } catch (e) {}
      localStorage.removeItem('dockpal_token');
    }
  },

  async login() {
    this.loading = true;
    this.error = '';
    try {
      const resp = await fetch('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(this.loginForm)
      });
      const data = await resp.json();
      if (!resp.ok) { this.error = data.error || 'Login failed'; return; }
      this.token = data.token;
      this.username = data.username;
      this.userRole = data.role || '';
      localStorage.setItem('dockpal_token', this.token);
      this.view = 'app';
      this.currentPage = 'dashboard';
      // Load instances after login
      await this.loadInstances();
      await this.loadDashboard();
      await this.checkForUpdates();
    } catch (e) {
      this.error = 'Connection error';
    } finally {
      this.loading = false;
    }
  },

  logout() {
    localStorage.removeItem('dockpal_token');
    this.token = '';
    this.view = 'login';
    this.destroyChart();
    if (this.statsInterval) clearInterval(this.statsInterval);
    if (this.sysResourceInterval) clearInterval(this.sysResourceInterval);
    if (Dockpal._charts.cpu) { Dockpal._charts.cpu.destroy(); Dockpal._charts.cpu = null; }
    if (Dockpal._charts.ram) { Dockpal._charts.ram.destroy(); Dockpal._charts.ram = null; }
  },

  // Load all instances from the server (Requirement 10.5)
  async loadInstances() {
    try {
      const resp = await this.api('GET', '/api/instances');
      if (resp && resp.ok) {
        const data = await resp.json();
        const list = Array.isArray(data) ? data : (data.instances || []);
        const hasLocal = list.some(i => i.id === 'local');
        this.instances = hasLocal ? list : [{ id: 'local', name: 'This Server', status: 'online' }, ...list];
      }
    } catch (e) {
      console.error('Failed to load instances:', e);
      // On error, show local instance as default
      this.instances = [{ id: 'local', name: 'This Server', status: 'online' }];
    }
  },

  async api(method, path, body) {
    const opts = { method, headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token } };
    if (body) opts.body = JSON.stringify(body);
    const resp = await fetch(path, opts);
    if (resp.status === 401) { this.logout(); return null; }
    return resp;
  },
};
