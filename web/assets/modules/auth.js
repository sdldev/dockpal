// Authentication, session lifecycle, and authenticated API wrapper.
window.Dockpal = window.Dockpal || {};

Dockpal.auth = {
  async init() {
    const saved = localStorage.getItem('dockpal_token');
    if (saved) {
      this.token = saved;
      try {
        const resp = await fetch('/api/containers', { headers: { Authorization: 'Bearer ' + this.token } });
        if (resp.ok) {
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
      localStorage.setItem('dockpal_token', this.token);
      this.view = 'app';
      this.currentPage = 'dashboard';
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

  async api(method, path, body) {
    const opts = { method, headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token } };
    if (body) opts.body = JSON.stringify(body);
    const resp = await fetch(path, opts);
    if (resp.status === 401) { this.logout(); return null; }
    return resp;
  },
};
