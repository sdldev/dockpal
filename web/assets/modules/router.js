// Client-side SPA router: URL ↔ currentPage sync with pushState/popstate.
window.Dockpal = window.Dockpal || {};

Dockpal.router = {
  // Map URL paths to page IDs. Pages with dynamic segments (container-detail,
  // template-config) are handled via path prefix matching.
  pathToPage(path) {
    const p = path.replace(/^\/+|\/+$/g, '') || 'dashboard';
    if (p === 'instances' || p === 'instances/add') return 'add-instance';
    if (p.startsWith('containers/') && p.split('/').length === 2) return 'container-detail';
    if (p.startsWith('templates/') && p.split('/').length === 2) return 'template-config';
    const valid = ['dashboard','fleet','containers','deploy','templates','images','apps',
                   'files','domains','registry','instances','profile'];
    return valid.includes(p) ? p : null;
  },

  // Build URL path from page ID + optional param (container ID / template ID).
  pageToPath(pageId) {
    if (pageId === 'add-instance') return '/instances/add';
    if (pageId === 'container-detail' && this.selectedContainer) {
      return '/containers/' + this.selectedContainer.id;
    }
    if (pageId === 'template-config' && this.templateConfig?.template?.id) {
      return '/templates/' + this.templateConfig.template.id;
    }
    return '/' + pageId;
  },

  // Navigate to a page — updates state, URL, and loads page data.
  navigateTo(pageId, replace) {
    if (pageId === this.currentPage) return;
    // Tear down page-specific resources before switching.
    if (pageId !== 'container-detail') this.cleanupContainerDetail();
    this.currentPage = pageId;
    this.sidebarOpen = false;
    const url = this.pageToPath(pageId);
    if (replace) {
      window.history.replaceState({ page: pageId }, '', url);
    } else {
      window.history.pushState({ page: pageId }, '', url);
    }
    this.loadPageData(pageId);
  },

  // Load data relevant to the target page on navigation.
  loadPageData(pageId) {
    switch (pageId) {
      case 'dashboard':  if (this.loadDashboard) this.loadDashboard(); break;
      case 'fleet':      if (this.loadFleet) this.loadFleet(); break;
      case 'containers': if (this.loadContainers) this.loadContainers(); break;
      case 'images':     if (this.loadImages) this.loadImages(); break;
      case 'apps':       if (this.loadApps) this.loadApps(); break;
      case 'templates':  if (this.loadTemplates) this.loadTemplates(); break;
      case 'domains':    if (this.loadDomains) this.loadDomains(); break;
      case 'services':   if (this.loadServices) this.loadServices(); break;
      case 'instances':  if (this.loadInstances) this.loadInstances(); break;
      case 'profile':    if (this.loadProfile) this.loadProfile(); break;
      case 'registry':   if (this.loadRegistries) this.loadRegistries(); break;
    }
  },

  // Initialize router: parse current URL and set initial page.
  // Called from auth.init() after token validation.
  initRouter() {
    const path = window.location.pathname;
    const page = this.pathToPage(path);
    if (page) {
      this.currentPage = page;
      // Load page-specific data for the resolved page.
      this.loadPageData(page);
      // For container-detail, extract the ID from URL
      if (page === 'container-detail') {
        const id = path.replace(/^\/+|\/+$/g, '').split('/')[1];
        if (id && this.containers.length) {
          const c = this.containers.find(c => c.id === id || c.id.startsWith(id));
          if (c) this.selectedContainer = c;
        }
      }
      // For template-config, extract the ID from URL
      if (page === 'template-config') {
        const id = path.replace(/^\/+|\/+$/g, '').split('/')[1];
        if (id && this.templates.length) {
          const t = this.templates.find(t => t.id === id);
          if (t) this.configureTemplate(t);
        }
      }
    } else {
      this.currentPage = 'dashboard';
      window.history.replaceState({ page: 'dashboard' }, '', '/dashboard');
    }
  },
};

// Global popstate listener — placed here so it's registered once.
if (!window.__dockpalPopstateBound) {
  window.__dockpalPopstateBound = true;
  window.addEventListener('popstate', function(event) {
    const D = window.Dockpal;
    if (!D || !D._app) return;
    const app = D._app;
    if (event.state && event.state.page) {
      app.currentPage = event.state.page;
      app.loadPageData(event.state.page);
    } else {
      const path = window.location.pathname;
      const page = D.router.pathToPage(path);
      if (page && page !== app.currentPage) {
        app.currentPage = page;
        app.loadPageData(page);
      }
    }
  });
}