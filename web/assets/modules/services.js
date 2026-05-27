// Services: deploy from compose/git, list, delete, GitHub repos.
window.Dockpal = window.Dockpal || {};

Dockpal.services = {
  async deployCompose() {
    // Use instanceApi for compose deploy
    const resp = await this.instanceApi('POST', '/deploy/compose', this.deployForm);
    if (resp && resp.ok) {
      this.toast('Stack deployed', 'success');
      this.deployForm = { name: '', domain: '', compose: '' };
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
      const payload = {
        repo: this.gitForm.repo,
        branch: this.gitForm.branch,
        compose_file: this.gitForm.compose_file || '',
        name: this.gitForm.name || ''
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
        this.gitForm = { repo: '', branch: 'main', compose_file: '', name: '' };
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
