// Authentication, session lifecycle, and authenticated API wrapper.
window.Dockpal = window.Dockpal || {};

Dockpal.auth = {
  // Fetch the public client-facing configuration (auto_update_enabled,
  // future flags) once at boot — both before login (so the login screen
  // can render the right shell) and on resume of an existing session.
  // Failures are swallowed because the endpoint is best-effort: a
  // network error or a server too old to expose /api/config simply
  // leaves the flag at its default ("true"), matching the long-standing
  // behaviour of the UI before this flag existed (R7.1).
  async loadFeatureConfig() {
    try {
      const resp = await fetch('/api/config');
      if (resp && resp.ok) {
        const data = await resp.json();
        this.featureAutoUpdate = data && data.auto_update_enabled !== false;
        return;
      }
    } catch (e) { /* ignore — fall through to default */ }
    this.featureAutoUpdate = true;
  },

  async init() {
    // Load feature flags first so the rest of init() (and any pre-login
    // render) can depend on them.
    await this.loadFeatureConfig();

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
          // Open the App_Update_Feed (R4.4) and seed the apps list so the
          // sidebar badge / Apps page have data on first navigation.
          // Skip when the server has pinned auto-update off — there is
          // no worker to publish events and no toggle to surface.
          if (this.featureAutoUpdate) {
            if (this.loadApps) await this.loadApps();
            if (this.startFeed) this.startFeed();
          }
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
          if (this.featureAutoUpdate) {
            if (this.loadApps) await this.loadApps();
            if (this.startFeed) this.startFeed();
          }
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
      // Open the App_Update_Feed (R4.4). We also kick off loadApps() so the
      // first SSE event has a target row to mutate. Skip when the server
      // has pinned auto-update off (R7.1) — no worker, no toggle.
      if (this.featureAutoUpdate) {
        if (this.loadApps) await this.loadApps();
        if (this.startFeed) this.startFeed();
      }
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
    this.cleanupSessionResources();
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
    if (body !== undefined && body !== null) opts.body = JSON.stringify(body);
    const resp = await fetch(path, opts);
    if (resp.status === 401) { this.logout(); return null; }
    return resp;
  },
};
