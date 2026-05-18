// Computed getters: filtered lists, derived data.
window.Dockpal = window.Dockpal || {};

Dockpal.computed = {
  get currentNavLabel() {
    const item = this.navItems.find(n => n.id === this.currentPage);
    return item ? item.label : 'Dashboard';
  },

  get filteredContainers() {
    const f = this.filters.containers;
    const q = f.search.toLowerCase().trim();
    return this.containers.filter(c => {
      if (f.state !== 'all' && c.state !== f.state) return false;
      if (!q) return true;
      return (c.name || '').toLowerCase().includes(q) ||
             (c.image || '').toLowerCase().includes(q) ||
             (c.id || '').toLowerCase().includes(q);
    }).map(c => ({ ...c, ports: this.dedupePorts(c.ports) }));
  },

  get filteredTemplates() {
    const f = this.filters.templates;
    const q = f.search.toLowerCase().trim();
    return this.templates.filter(t => {
      if (f.category !== 'all' && t.category !== f.category) return false;
      if (!q) return true;
      return (t.name || '').toLowerCase().includes(q) ||
             (t.description || '').toLowerCase().includes(q) ||
             (t.category || '').toLowerCase().includes(q);
    });
  },

  get templateCategories() {
    const cats = new Set(this.templates.map(t => t.category).filter(Boolean));
    return Array.from(cats).sort();
  },

  get filteredImages() {
    const q = this.filters.images.search.toLowerCase().trim();
    if (!q) return this.images;
    return this.images.filter(i =>
      (i.repo || '').toLowerCase().includes(q) ||
      (i.tag || '').toLowerCase().includes(q) ||
      (i.id || '').toLowerCase().includes(q)
    );
  },

  get filteredDomains() {
    const q = this.filters.domains.search.toLowerCase().trim();
    if (!q) return this.domains;
    return this.domains.filter(d =>
      (d.domain || '').toLowerCase().includes(q) ||
      (d.service || '').toLowerCase().includes(q)
    );
  },

  get parsedEnv() {
    if (!this.selectedContainer || !this.selectedContainer.env) return [];
    const secretPatterns = /password|passwd|secret|token|key|api_key|apikey|auth|credential/i;
    return this.selectedContainer.env.map(line => {
      const idx = line.indexOf('=');
      const key = idx > 0 ? line.slice(0, idx) : line;
      const value = idx > 0 ? line.slice(idx + 1) : '';
      return { key, value, isSecret: secretPatterns.test(key) };
    }).sort((a, b) => a.key.localeCompare(b.key));
  },

  copyEnvAsDotenv() {
    const text = this.parsedEnv.map(e => e.key + '=' + e.value).join('\n');
    this.copyToClipboard(text);
    this.toast('Environment copied to clipboard', 'success');
  },
};
