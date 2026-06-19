// Services: deploy from compose/git, list, delete, GitHub repos.
window.Dockpal = window.Dockpal || {};

Dockpal.services = {
  parseEnvText(envText) {
    const env = {};
    const lines = (envText || '').split('\n');
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i].trim();
      if (!line || line.startsWith('#')) continue;
      const equalsIndex = line.indexOf('=');
      if (equalsIndex <= 0) throw new Error(`Invalid env line ${i + 1}: expected KEY=value`);
      const key = line.slice(0, equalsIndex).trim();
      const value = line.slice(equalsIndex + 1);
      if (!key) throw new Error(`Invalid env line ${i + 1}: empty key`);
      env[key] = value;
    }
    return env;
  },

  async deployCompose() {
    let env;
    try {
      env = this.parseEnvText(this.deployForm.env_text);
    } catch (e) {
      this.toast(e.message || 'Invalid environment variables', 'error', 5000);
      return;
    }
    const payload = { ...this.deployForm, env };
    delete payload.env_text;
    // Use instanceApi for compose deploy
    const resp = await this.instanceApi('POST', '/deploy/compose', payload);
    if (resp && resp.ok) {
      this.toast('Stack deployed', 'success');
      this.deployForm = { name: '', domain: '', compose: '', auto_start: true, env_text: '' };
      this.navigateTo('containers');
    } else {
      const data = resp ? await resp.json().catch(() => ({})) : {};
      this.toast(data.error || 'Deploy failed', 'error', 5000);
    }
    await this.loadDashboard();
    await this.loadServices();
  },

  async deployGit() {
    this.gitDeploying = true;
    try {
      let env;
      try {
        env = this.parseEnvText(this.gitForm.env_text);
      } catch (e) {
        this.toast(e.message || 'Invalid environment variables', 'error', 5000);
        return;
      }
      const payload = {
        repo: this.gitForm.repo,
        branch: this.gitForm.branch,
        compose_file: this.gitForm.compose_file || '',
        name: this.gitForm.name || '',
        env
      };
      // Use instanceApi for git deploy
      const resp = await this.instanceApi('POST', '/deploy/git', payload);
      if (resp && resp.ok) {
        const data = await resp.json().catch(() => ({}));
        if (data.status === 'select_compose') {
          this.composeFiles = data.compose_files || [];
          this.gitForm.compose_file = this.composeFiles[0] || '';
          this.toast('Select a compose file to deploy', 'info', 3000);
          return;
        }
        this.toast('Deployed successfully', 'success');
        this.gitForm = { repo: '', branch: 'main', compose_file: '', name: '', env_text: '' };
        this.githubSearch = '';
        this.composeFiles = [];
        this.navigateTo('containers');
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.toast(data.error || 'Deploy failed', 'error', 5000);
      }
      await this.loadDashboard();
      await this.loadServices();
    } finally {
      this.gitDeploying = false;
    }
  },

  async loadGithubRepos() {
    // This is a global endpoint, not instance-scoped
    this.githubLoading = true;
    this.githubError = '';
    try {
      const resp = await this.api('GET', '/api/github/repos?per_page=100');
      if (resp && resp.ok) {
        this.githubRepos = await resp.json();
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.githubError = data.error || 'Failed to load repositories';
        this.githubRepos = [];
      }
    } catch (e) {
      this.githubError = 'Failed to connect';
      this.githubRepos = [];
    } finally {
      this.githubLoading = false;
    }
  },

  selectGithubRepo(repo) {
    this.gitForm.repo = repo.clone_url;
    this.gitForm.branch = repo.default_branch || 'main';
    this.gitForm.compose_file = '';
    this.gitForm.name = '';
    this.gitForm.env_text = '';
    this.composeFiles = [];
  },

  async loadServices() {
    // Use instanceApi for instance-scoped service listing
    const resp = await this.instanceApi('GET', '/services');
    if (resp) this.services = await resp.json();
  },

  deleteService(id) {
    const svc = this.services.find(s => s.id === id);
    this.showConfirm({
      title: 'Delete Service',
      message: 'Remove "' + (svc?.name || id) + '"? This will stop and remove all containers in this stack along with the compose configuration.',
      confirmText: 'Delete',
      onConfirm: async () => {
        // Use instanceApi for service deletion
        const resp = await this.instanceApi('DELETE', '/services/' + id);
        if (resp && !resp.ok) {
          const data = await resp.json().catch(() => ({}));
          this.toast(data.error || 'Failed to delete service', 'error', 5000);
        } else {
          this.toast('Service deleted', 'success');
        }
        await this.loadServices();
      }
    });
  },
};
