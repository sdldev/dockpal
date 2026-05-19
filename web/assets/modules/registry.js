// Registry credentials: list, add, delete, test connection.
window.Dockpal = window.Dockpal || {};

Dockpal.registry = {
  async loadRegistries() {
    this.registryLoading = true;
    try {
      // Use instanceApi for instance-scoped registry listing
      const resp = await this.instanceApi('GET', '/registries');
      if (resp && resp.ok) this.registries = await resp.json();
    } finally {
      this.registryLoading = false;
    }
  },

  validateRegistryForm() {
    this.registryFormErrors = { registry: '', username: '', token: '' };
    let valid = true;

    if (!this.registryForm.registry.trim()) {
      this.registryFormErrors.registry = 'Registry URL is required';
      valid = false;
    } else if (this.registryForm.registry.trim().length > 253) {
      this.registryFormErrors.registry = 'Registry URL must be 253 characters or less';
      valid = false;
    }

    if (!this.registryForm.username.trim()) {
      this.registryFormErrors.username = 'Username is required';
      valid = false;
    } else if (this.registryForm.username.trim().length > 100) {
      this.registryFormErrors.username = 'Username must be 100 characters or less';
      valid = false;
    }

    if (!this.registryForm.token.trim()) {
      this.registryFormErrors.token = 'Token is required';
      valid = false;
    } else if (this.registryForm.token.trim().length > 255) {
      this.registryFormErrors.token = 'Token must be 255 characters or less';
      valid = false;
    }

    return valid;
  },

  async addRegistry() {
    if (!this.validateRegistryForm()) return;

    this.registryLoading = true;
    try {
      // Use instanceApi for adding registry credentials
      const resp = await this.instanceApi('POST', '/registries', {
        registry: this.registryForm.registry.trim(),
        username: this.registryForm.username.trim(),
        token: this.registryForm.token.trim()
      });
      if (resp && resp.ok) {
        this.toast('Registry credential added', 'success');
        this.registryForm = { registry: 'ghcr.io', username: '', token: '' };
        this.registryFormVisible = false;
        this.registryFormErrors = { registry: '', username: '', token: '' };
        await this.loadRegistries();
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.toast(data.error || 'Failed to add registry credential', 'error', 5000);
      }
    } finally {
      this.registryLoading = false;
    }
  },

  deleteRegistry(id) {
    const reg = this.registries.find(r => r.id === id);
    this.showConfirm({
      title: 'Delete Registry Credential',
      message: 'Remove credentials for "' + (reg?.registry || id) + '"? This cannot be undone.',
      confirmText: 'Delete',
      onConfirm: async () => {
        // Use instanceApi for deleting registry credentials
        const resp = await this.instanceApi('DELETE', '/registries/' + id);
        if (resp && resp.ok) {
          this.toast('Registry credential deleted', 'success');
        } else {
          const data = resp ? await resp.json().catch(() => ({})) : {};
          this.toast(data.error || 'Failed to delete credential', 'error', 5000);
        }
        await this.loadRegistries();
      }
    });
  },

  async testRegistryConnection(id) {
    this.registryTestResult = null;
    this.registryTesting = id;
    try {
      // Use instanceApi for testing registry connections
      const resp = await this.instanceApi('POST', '/registries/' + id + '/test');
      if (resp && resp.ok) {
        const data = await resp.json();
        this.registryTestResult = { id, ...data };
        if (data.status === 'valid') {
          this.toast('Connection successful', 'success');
          await this.loadRegistries();
        } else {
          this.toast(data.message || 'Connection failed', 'error', 5000);
        }
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.toast(data.error || 'Connection test failed', 'error', 5000);
      }
    } finally {
      this.registryTesting = null;
    }
  },
};
