// Initial Alpine state and shared data structures.
// Returns the base state object that gets merged with module behaviors.
window.Dockpal = window.Dockpal || {};

Dockpal.initialState = function() {
  return {
    view: 'login',
    currentPage: 'dashboard',
    sidebarOpen: false,
    token: '',
    username: '',
    error: '',
    loading: false,

    containers: [],
    containerDetailTab: 'overview',
    envMaskSecrets: true,
    selectedContainer: null,
    containerStats: null,
    logs: [],
    statsInterval: null,
    statsHistory: { cpu: [], mem: [], rx: [], tx: [], labels: [] },

    systemInfo: null,
    sysResourceInterval: null,
    sysResourceHistory: { labels: [], cpu: [], ram: [] },

    loginForm: { username: '', password: '' },
    deployForm: { name: '', domain: '', compose: '' },
    gitForm: { repo: '', branch: 'main' },
    services: [],

    templates: [],
    templateConfig: {
      template: null, name: '', env: {}, envText: '', envMode: 'form',
      ports: {}, restartPolicy: 'unless-stopped', networkMode: 'bridge',
      customNetwork: '', autoRecover: false, domain: '', logs: [],
      deploying: false, error: '', activeTab: 'environment'
    },

    images: [],
    imageCount: 0,
    imagePullName: '',

    domains: [],
    domainForm: { name: '', service: '', port: 80 },

    fileManager: { containerId: '', path: '/', files: [] },

    registries: [],
    registryForm: { registry: 'ghcr.io', username: '', token: '' },
    registryFormVisible: false,
    registryFormErrors: { registry: '', username: '', token: '' },
    registryTestResult: null,
    registryLoading: false,
    registryTesting: null,

    confirmDialog: { show: false, title: '', message: '', confirmText: 'Delete', danger: true, onConfirm: null },
    toasts: [],

    filters: {
      containers: { search: '', state: 'all' },
      templates: { search: '', category: 'all' },
      images: { search: '' },
      domains: { search: '' }
    },

    // Update banner state
    updateAvailable: false,
    updateVersion: '',
    updateReleaseNotes: '',
    updateDownloadUrl: '',
    updateDismissed: false,
    updateProgress: null,
    currentVersion: '',

    navItems: [
      { id: 'dashboard', label: 'Dashboard', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 5a1 1 0 011-1h4a1 1 0 011 1v5a1 1 0 01-1 1H5a1 1 0 01-1-1V5zm10 0a1 1 0 011-1h4a1 1 0 011 1v3a1 1 0 01-1 1h-4a1 1 0 01-1-1V5zM4 16a1 1 0 011-1h4a1 1 0 011 1v3a1 1 0 01-1 1H5a1 1 0 01-1-1v-3zm10-2a1 1 0 011-1h4a1 1 0 011 1v5a1 1 0 01-1 1h-4a1 1 0 01-1-1v-5z"/></svg>' },
      { type: 'group', label: 'Applications' },
      { id: 'deploy', label: 'Deploy', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12"/></svg>' },
      { id: 'templates', label: 'Templates', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10"/></svg>' },
      { type: 'group', label: 'Infrastructure' },
      { id: 'containers', label: 'Containers', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4"/></svg>' },
      { id: 'images', label: 'Images', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"/></svg>' },
      { id: 'files', label: 'Volumes', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"/></svg>' },
      { type: 'group', label: 'Networking' },
      { id: 'domains', label: 'Domains', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9m-9 9a9 9 0 019-9"/></svg>' },
      { type: 'group', label: 'Settings' },
      { id: 'registry', label: 'Registry', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z"/></svg>' },
    ],
  };
};
