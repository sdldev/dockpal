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
    userRole: '',
    error: '',
    instanceError: '',  // Error message for instance unavailability
    loading: false,

    // Instance state (Requirements 10.4, 10.5, 12.1)
    instances: [],
    selectedInstance: localStorage.getItem('dockpal_selected_instance') || 'local',

    containers: [],
    containerDetailTab: 'overview',
    envMaskSecrets: true,
    selectedContainer: null,
    containerStats: null,
    logs: [],
    statsInterval: null,
    statsHistory: { cpu: [], mem: [], rx: [], tx: [], labels: [] },
    containerEditMode: false,
    containerEditSaving: false,
    editContainerForm: { name: '', image: '', restart_policy: '', memory_mb: 0, cpu_limit: 0, env: [], ports: [], volumes: [] },

    systemInfo: null,
    sysResourceInterval: null,
    sysResourceHistory: { labels: [], cpu: [], ram: [] },

    loginForm: { username: '', password: '' },
    deployForm: { name: '', domain: '', compose: '' },
    gitForm: { repo: '', branch: 'main', compose_file: '', name: '' },
    deploySource: 'github',
    githubRepos: [],
    githubLoading: false,
    githubError: '',
    githubSearch: '',
    composeFiles: [],
    gitDeploying: false,
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

    instanceForm: { name: '', mode: 'direct', host: '', port: 9273, installCommand: '' },
    installCommandModal: { show: false, command: '', instanceId: '' },

    // Profile state
    profileLoading: false,
    profile: null,
    changePasswordForm: { current_password: '', new_password: '', confirm_password: '' },
    changePasswordError: '',
    changePasswordSuccess: '',
    users: [],
    usersLoading: false,

    // Update modal state
    updateAvailable: false,
    updateVersion: '',
    updateReleaseNotes: '',
    updateDownloadUrl: '',
    updateDismissed: false,
    updateModalVisible: false,
    updateProgress: null,
    updateChecking: false,
    currentVersion: '',

    // Fleet state
    fleetTab: 'overview',
    fleetInstances: [],
    fleetContainers: [],
    fleetContainerSearch: '',
    bulkDeployForm: { name: '', compose: '', targets: [] },
    bulkDeploying: false,
    bulkDeployLogs: [],

    navItems: [
      { id: 'dashboard', label: 'Dashboard', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 5a1 1 0 011-1h4a1 1 0 011 1v5a1 1 0 01-1 1H5a1 1 0 01-1-1V5zm10 0a1 1 0 011-1h4a1 1 0 011 1v3a1 1 0 01-1 1h-4a1 1 0 01-1-1V5zM4 16a1 1 0 011-1h4a1 1 0 011 1v3a1 1 0 01-1 1H5a1 1 0 01-1-1v-3zm10-2a1 1 0 011-1h4a1 1 0 011 1v5a1 1 0 01-1 1h-4a1 1 0 01-1-1v-5z"/></svg>' },
      { id: 'fleet', label: 'Fleet Dashboard', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6zM14 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V6zM4 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zM14 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2v-2z"/></svg>' },
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
      { id: 'instances', label: 'Instances', icon: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01"/></svg>' },
    ],
  };
};

// Instance management module (Requirements 10.4, 10.5, 12.1)
// This will be merged into the initial state in app.js
Dockpal.instances = {
  // Compute if selected instance is local
  get isLocalInstance() {
    return this.selectedInstance === 'local';
  },

  // Instance API helper - prepends instance path to resource path
  // Uses the auth.api method for making requests
  async instanceApi(method, path, body) {
    const instancePath = `/api/instances/${this.selectedInstance}${path}`;
    let resp;
    // Call auth.api which is available as 'this.api' in the merged target
    if (this.api) {
      resp = await this.api(method, instancePath, body);
    } else {
      // Fallback to Dockpal.auth.api if not in target context
      resp = await Dockpal.auth.api(method, instancePath, body);
    }
    
    // Handle instance offline/unreachable errors (Requirement 12.11)
    if (resp && (resp.status === 503 || resp.status === 404)) {
      const errorData = await resp.json().catch(() => ({}));
      const errorMsg = errorData.error || 'Instance unavailable';
      
      // Show toast error
      if (this.toast) {
        this.toast(errorMsg + ' - ' + (this.selectedInstance === 'local' ? 'Local server' : 'Remote instance'), 'error', 5000);
      }
      
      // Set instance error state for display in page
      this.instanceError = errorMsg;
    } else if (resp && resp.ok) {
      // Clear error on successful response
      this.instanceError = '';
    }
    
    return resp;
  },

  // Select an instance and reload the dashboard
  async selectInstance(instanceId) {
    this.selectedInstance = instanceId;
    localStorage.setItem('dockpal_selected_instance', instanceId);
    
    // Reset page state and reload dashboard
    this.containers = [];
    this.systemInfo = null;
    this.images = [];
    this.domains = [];
    this.templates = [];
    this.services = [];
    
    // Reload dashboard data using the same method as auth module
    if (this.loadDashboard) {
      await this.loadDashboard();
    }
  },

  // Load all instances from the server
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
      // On error, keep default local instance
      this.instances = [{ id: 'local', name: 'This Server', status: 'online' }];
    }
  },

  navigateToAddInstance() {
    this.instanceForm = {
      name: '',
      mode: 'direct',
      host: '',
      port: 9273,
      installCommand: '',
      autoInstall: false,
      sshHost: '',
      sshPort: 22,
      sshUser: 'root',
      sshAuthType: 'password',
      sshSecret: '',
      installDocker: true,
      installing: false,
      installLogs: []
    };
    this.currentPage = 'add-instance';
  },

  async addInstance() {
    const form = this.instanceForm;
    const payload = {
      name: form.name,
      mode: form.mode
    };
    
    if (form.mode === 'direct') {
      payload.host = form.host;
      payload.port = form.port || 9273;
    }

    try {
      const resp = await this.api('POST', '/api/instances', payload);
      if (resp && resp.ok) {
        const data = await resp.json();
        
        if (form.autoInstall) {
          this.instanceForm.installing = true;
          this.startSSHInstall(data.id);
        } else {
          // Show install command
          this.instanceForm.installCommand = data.install_command || '';
        }
        
        // Reload instances list
        await this.loadInstances();
      } else {
        const data = await resp.json().catch(() => ({}));
        this.toast(data.error || 'Failed to add instance', 'error', 5000);
      }
    } catch (e) {
      this.toast('Failed to connect to server', 'error', 5000);
    }
  },

  startSSHInstall(instanceId) {
    this.instanceForm.installLogs = [];
    this.instanceForm.installLogs.push('[Dockpal] Connecting to logs channel...');
    
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/instances/${instanceId}/install/logs`;
    
    const ws = new WebSocket(wsUrl);
    
    ws.onmessage = (event) => {
      this.instanceForm.installLogs.push(event.data);
      setTimeout(() => {
        const terminal = document.getElementById('install-terminal-logs');
        if (terminal) {
          terminal.scrollTop = terminal.scrollHeight;
        }
      }, 30);
    };
    
    ws.onerror = (err) => {
      this.instanceForm.installLogs.push('[Dockpal] WebSocket error connecting to logs stream.');
    };
    
    ws.onclose = () => {
      this.instanceForm.installLogs.push('[Dockpal] Disconnected from logs channel.');
      this.instanceForm.installing = false;
      this.loadInstances();
    };
    
    const payload = {
      ssh_host: this.instanceForm.sshHost || this.instanceForm.host,
      ssh_port: parseInt(this.instanceForm.sshPort) || 22,
      ssh_user: this.instanceForm.sshUser || 'root',
      ssh_auth_type: this.instanceForm.sshAuthType,
      ssh_secret: this.instanceForm.sshSecret,
      install_docker: this.instanceForm.installDocker
    };
    
    this.api('POST', `/api/instances/${instanceId}/install`, payload)
      .then(async (resp) => {
        if (resp && resp.ok) {
          this.toast('SSH agent installation started', 'success');
        } else {
          const data = await resp.json().catch(() => ({}));
          this.instanceForm.installLogs.push(`[Dockpal] Error starting installation: ${data.error || 'Unknown error'}`);
          this.instanceForm.installing = false;
          ws.close();
        }
      })
      .catch((err) => {
        this.instanceForm.installLogs.push(`[Dockpal] Connection error: ${err.message || 'Failed to connect'}`);
        this.instanceForm.installing = false;
        ws.close();
      });
  },

  async testInstanceConnection(instanceId) {
    this.toast('Testing connection...', 'info', 2000);
    try {
      const resp = await this.api('POST', '/api/instances/' + instanceId + '/test');
      if (resp && resp.ok) {
        const data = await resp.json();
        if (data.status === 'ok') {
          this.toast('Connection successful', 'success');
        } else {
          this.toast(data.message || 'Connection failed', 'error', 5000);
        }
      } else {
        const data = await resp.json().catch(() => ({}));
        this.toast(data.error || 'Connection test failed', 'error', 5000);
      }
    } catch (e) {
      this.toast('Connection test failed', 'error', 5000);
    }
    // Reload instances to get updated status
    await this.loadInstances();
  },

  removeInstance(instanceId) {
    const inst = this.instances.find(i => i.id === instanceId);
    this.showConfirm({
      title: 'Remove Instance',
      message: 'Remove "' + (inst?.name || instanceId) + '"? This will stop managing this Docker host.',
      confirmText: 'Remove',
      onConfirm: async () => {
        try {
          const resp = await this.api('DELETE', '/api/instances/' + instanceId);
          if (resp && resp.ok) {
            this.toast('Instance removed', 'success');
            // If we removed the selected instance, switch to local
            if (this.selectedInstance === instanceId) {
              this.selectedInstance = 'local';
              localStorage.setItem('dockpal_selected_instance', 'local');
            }
            await this.loadInstances();
          } else {
            const data = await resp.json().catch(() => ({}));
            this.toast(data.error || 'Failed to remove instance', 'error', 5000);
          }
        } catch (e) {
          this.toast('Failed to remove instance', 'error', 5000);
        }
      }
    });
  },

  copyInstallCommand() {
    const cmd = this.instanceForm.installCommand;
    if (cmd) {
      navigator.clipboard.writeText(cmd).then(() => {
        this.toast('Copied to clipboard', 'success', 2000);
      }).catch(() => {
        this.toast('Failed to copy', 'error', 2000);
      });
    }
  },

  // Format relative time (e.g., "2 minutes ago")
  formatRelativeTime(timestamp) {
    if (!timestamp) return '—';
    const now = Math.floor(Date.now() / 1000);
    const diff = now - timestamp;
    if (diff < 60) return 'just now';
    if (diff < 3600) return Math.floor(diff / 60) + ' minutes ago';
    if (diff < 86400) return Math.floor(diff / 3600) + ' hours ago';
    return Math.floor(diff / 86400) + ' days ago';
  },

  async showInstallCommand(instanceId) {
    try {
      const resp = await this.api('GET', '/api/instances/' + instanceId);
      if (resp && resp.ok) {
        const data = await resp.json();
        this.installCommandModal = {
          show: true,
          command: data.install_command || 'No install command available. Try rotating the token.',
          instanceId: instanceId
        };
      } else {
        this.toast('Failed to get instance details', 'error', 5000);
      }
    } catch (e) {
      this.toast('Failed to get instance details', 'error', 5000);
    }
  },

  async rotateInstanceToken(instanceId) {
    const inst = this.instances.find(i => i.id === instanceId);
    this.showConfirm({
      title: 'Rotate Agent Token',
      message: 'Rotate the token for "' + (inst?.name || instanceId) + '"? The agent will need to be re-installed with the new token.',
      confirmText: 'Rotate',
      onConfirm: async () => {
        try {
          const resp = await this.api('POST', '/api/instances/' + instanceId + '/rotate-token');
          if (resp && resp.ok) {
            const data = await resp.json();
            this.toast('Token rotated successfully', 'success');
            this.installCommandModal = {
              show: true,
              command: data.install_command || '',
              instanceId: instanceId
            };
          } else {
            const data = await resp.json().catch(() => ({}));
            this.toast(data.error || 'Failed to rotate token', 'error', 5000);
          }
        } catch (e) {
          this.toast('Failed to rotate token', 'error', 5000);
        }
      }
    });
  },

  copyToClipboard(text) {
    if (text) {
      navigator.clipboard.writeText(text).then(() => {
        this.toast('Copied to clipboard', 'success', 2000);
      }).catch(() => {
        this.toast('Failed to copy', 'error', 2000);
      });
    }
  },
};
