// Templates: library, deploy config page, env editor, streamed deploy.
window.Dockpal = window.Dockpal || {};

Dockpal.templates = {
  async loadTemplates() {
    // Templates are global (not instance-scoped) - they are defined on the server
    const resp = await this.api('GET', '/api/templates');
    if (resp) this.templates = await resp.json();
  },

  deployTemplate(t) {
    const env = {};
    const ports = {};
    (t.env_required || []).forEach(k => env[k] = '');
    (t.ports || []).forEach(p => ports[p.container_port] = p.default);
    this.templateConfig = {
      template: t,
      name: t.id + '-' + Date.now().toString().slice(-6),
      env,
      envText: this.envToText(env),
      envMode: 'form',
      ports,
      restartPolicy: 'unless-stopped',
      networkMode: 'bridge',
      customNetwork: '',
      autoRecover: false,
      domain: '',
      logs: [],
      deploying: false,
      error: '',
      activeTab: 'environment'
    };
    this.currentPage = 'template-config';
  },

  envToText(env) {
    return Object.entries(env).map(([k, v]) => k + '=' + (v || '')).join('\n');
  },

  envFromText(text) {
    const result = {};
    text.split('\n').forEach(line => {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) return;
      const eqIdx = trimmed.indexOf('=');
      if (eqIdx < 1) return;
      const key = trimmed.slice(0, eqIdx).trim();
      const value = trimmed.slice(eqIdx + 1).trim();
      if (key) result[key] = value;
    });
    return result;
  },

  switchEnvMode(mode) {
    const tc = this.templateConfig;
    if (mode === 'text' && tc.envMode === 'form') {
      tc.envText = this.envToText(tc.env);
    } else if (mode === 'form' && tc.envMode === 'text') {
      const parsed = this.envFromText(tc.envText);
      const required = tc.template.env_required || [];
      required.forEach(k => {
        if (parsed[k] !== undefined) tc.env[k] = parsed[k];
      });
    }
    tc.envMode = mode;
  },

  async deployFromConfig() {
    const tc = this.templateConfig;
    let env = tc.env;
    if (tc.envMode === 'text') env = this.envFromText(tc.envText);

    for (const k of (tc.template.env_required || [])) {
      if (!env[k] || !String(env[k]).trim()) {
        tc.error = k + ' is required';
        tc.activeTab = 'environment';
        return;
      }
    }
    for (const cp in tc.ports) {
      const p = parseInt(tc.ports[cp]);
      if (isNaN(p) || p < 1 || p > 65535) {
        tc.error = 'Port must be 1-65535';
        tc.activeTab = 'ports';
        return;
      }
    }

    tc.deploying = true;
    tc.error = '';
    tc.logs = [];
    tc.activeTab = 'logs';

    const portsInt = {};
    for (const cp in tc.ports) portsInt[cp] = parseInt(tc.ports[cp]);

    // Use instanceApi for template deploy (Requirements 12.4)
    const resp = await this.instanceApi('POST', '/templates/' + tc.template.id + '/deploy/stream', {
      env, ports: portsInt, custom_name: tc.name,
      restart_policy: tc.restartPolicy, auto_recover: tc.autoRecover, domain: tc.domain
    });
    if (!resp || !resp.ok) {
      const data = resp ? await resp.json() : {};
      tc.error = data.error || 'Deploy failed';
      tc.deploying = false;
      return;
    }
    const { deploy_id } = await resp.json();

    // Use instance-scoped WebSocket path (Requirement 12.8)
    const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(wsProto + '//' + location.host + '/api/instances/' + this.selectedInstance + '/deploy/stream/' + deploy_id + '?token=' + this.token);

    ws.onmessage = (e) => {
      const event = JSON.parse(e.data);
      tc.logs.push(event);
      this.$nextTick(() => {
        const el = document.getElementById('config-deploy-logs');
        if (el) el.scrollTop = el.scrollHeight;
      });
      if (event.status === 'error') {
        tc.error = event.message;
        tc.deploying = false;
      }
      if (event.step === 'complete') {
        tc.deploying = false;
        // Redirect to containers page after successful deploy
        setTimeout(() => {
          this.currentPage = 'containers';
          this.loadDashboard();
        }, 1500);
      }
    };
    ws.onerror = () => { tc.error = 'Connection lost'; tc.deploying = false; };
    ws.onclose = () => { if (tc.deploying) tc.deploying = false; };
  },
};
