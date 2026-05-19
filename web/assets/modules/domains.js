// Domains: list, add, delete (Traefik routing).
// Note: Domains are local-only features - requires isLocalInstance (Requirement 12.9)
window.Dockpal = window.Dockpal || {};

Dockpal.domains = {
  async loadDomains() {
    // Only load domains for local instance (local-only feature)
    if (!this.isLocalInstance) {
      this.domains = [];
      return;
    }
    const resp = await this.api('GET', '/api/domains');
    if (resp) this.domains = await resp.json();
  },

  async addDomain() {
    // Only allow domains for local instance
    if (!this.isLocalInstance) {
      this.toast('Domains are only available for local instance', 'error', 5000);
      return;
    }
    const resp = await this.api('POST', '/api/domains', this.domainForm);
    if (resp && resp.ok) {
      this.toast('Domain added', 'success');
      this.domainForm = { name: '', service: '', port: 80 };
    } else {
      const data = resp ? await resp.json().catch(() => ({})) : {};
      this.toast(data.error || 'Failed to add domain', 'error', 5000);
    }
    await this.loadDomains();
  },

  deleteDomain(id) {
    const dom = this.domains.find(d => d.id === id);
    this.showConfirm({
      title: 'Delete Domain',
      message: 'Remove domain "' + (dom?.domain || id) + '"? Traefik routing for this domain will be removed.',
      confirmText: 'Delete',
      onConfirm: async () => {
        const resp = await this.api('DELETE', '/api/domains/' + id);
        if (resp && !resp.ok) {
          const data = await resp.json().catch(() => ({}));
          this.toast(data.error || 'Failed to delete domain', 'error', 5000);
        } else {
          this.toast('Domain deleted', 'success');
        }
        await this.loadDomains();
      }
    });
  },
};
