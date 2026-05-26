// Apps: list, history, auto-update toggle, manual trigger, real-time SSE feed.
//
// Backs the apps.html page. Wires the App_Update_Feed (SSE) into the Alpine
// store so per-app status badges update in real time without polling.
//
// Endpoints (instance-scoped via instanceApi, see state.js):
//   GET   /apps                              → list with last_update + has_update
//   GET   /apps/:name/updates                → recent App_Update_Records
//   GET   /apps/:name/updates/:attemptID     → full record incl. event log
//   POST  /apps/:name/update                 → manual trigger, returns attempt_id
//   PATCH /apps/:name/auto-update            → toggle dockpal.auto-update label
//   GET   /apps/updates/stream               → SSE App_Update_Feed
//
// Toast handling for terminal stage events lives in handleFeedEvent: a
// `rolled_back` event surfaces a warning, `failed` an error, and `completed`
// a light success. Dedup uses the eventsByAttempt log so a stage repeated
// for the same attempt only toasts once.
window.Dockpal = window.Dockpal || {};

Dockpal.apps = {
  // ---- Data ----------------------------------------------------------------
  // These are own properties of the module object and get merged onto the
  // Alpine target by app.js via Object.defineProperties. They define the
  // initial state for the apps page.
  apps: [],
  selectedApp: null,
  appsLoading: false,
  appHistory: {},        // appName -> AppUpdateRecord[]
  attemptDetail: {},     // attemptID -> AppUpdateRecord (with full events)
  eventsByAttempt: {},   // attemptID -> AppUpdateFeedEvent[] (live stream)
  historyDetail: null,   // { app, attempt_id } — selected modal row, or null
  feedConnection: null,  // EventSource instance (or null when disconnected)

  // ---- Computed ------------------------------------------------------------
  // Search filter applied over `apps`. Matches name and any service name/image.
  // Returns the underlying list when the search box is empty so the page does
  // not pay for a filter pass on every render.
  get filteredApps() {
    const q = ((this.filters && this.filters.apps && this.filters.apps.search) || '').toLowerCase().trim();
    if (!q) return this.apps;
    return this.apps.filter(a => {
      if ((a.name || '').toLowerCase().includes(q)) return true;
      const svcs = a.services || [];
      for (const s of svcs) {
        if ((s.name || '').toLowerCase().includes(q)) return true;
        if ((s.image || '').toLowerCase().includes(q)) return true;
      }
      return false;
    });
  },

  // Count of apps currently in a transient update stage. Drives the sidebar
  // badge (R4.5) and the "Updating…" indicator in the table.
  get appsUpdating() {
    return (this.apps || []).filter(a =>
      a.update && ['pulling', 'recreating', 'verifying'].includes(a.update.stage)
    ).length;
  },

  // Count of apps with an available update that are NOT currently in a
  // transient stage. Pairs with appsUpdating to drive the dual sidebar
  // badge (R4.5): blue = updating now, amber = update available.
  get appsWithUpdates() {
    return (this.apps || []).filter(a => {
      if (!a || !a.has_update) return false;
      const stage = a.update && a.update.stage;
      return !['pulling', 'recreating', 'verifying'].includes(stage);
    }).length;
  },

  // ---- Loading -------------------------------------------------------------
  async loadApps() {
    this.appsLoading = true;
    try {
      const resp = await this.instanceApi('GET', '/apps');
      if (resp && resp.ok) {
        const list = await resp.json();
        // Normalise so the table can rely on `app.update` always existing.
        // The backend ships the most recent record on `last_update`; we
        // mirror its stage onto a transient `update` field so SSE events
        // can mutate it without losing the historical pointer.
        this.apps = (list || []).map(a => ({
          ...a,
          update: this._initialUpdateFor(a),
          _autoUpdateBusy: false,
        }));
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.toast(data.error || 'Failed to load apps', 'error', 5000);
      }
    } catch (e) {
      this.toast('Failed to load apps', 'error', 5000);
    } finally {
      this.appsLoading = false;
    }
  },

  async loadAppHistory(name) {
    if (!name) return;
    try {
      const resp = await this.instanceApi('GET', '/apps/' + encodeURIComponent(name) + '/updates?limit=50');
      if (resp && resp.ok) {
        const records = await resp.json();
        this.appHistory[name] = records || [];
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.toast(data.error || 'Failed to load history', 'error', 5000);
      }
    } catch (e) {
      this.toast('Failed to load history', 'error', 5000);
    }
  },

  async loadAttemptDetail(name, attemptID) {
    if (!name || !attemptID) return;
    try {
      const resp = await this.instanceApi(
        'GET',
        '/apps/' + encodeURIComponent(name) + '/updates/' + encodeURIComponent(attemptID)
      );
      if (resp && resp.ok) {
        const rec = await resp.json();
        this.attemptDetail[attemptID] = rec;
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.toast(data.error || 'Failed to load attempt', 'error', 5000);
      }
    } catch (e) {
      this.toast('Failed to load attempt', 'error', 5000);
    }
  },

  // ---- Mutations -----------------------------------------------------------
  // Optimistic toggle: flip `auto_update` immediately, set the row busy flag,
  // and revert on a non-2xx response. The PATCH triggers a no-pull recreate
  // server-side so the running container picks up the new label (R1.4).
  async toggleAutoUpdate(app, enabled) {
    if (!app || app._autoUpdateBusy) return;
    const prev = app.auto_update;
    app.auto_update = !!enabled;
    app._autoUpdateBusy = true;
    try {
      const resp = await this.instanceApi(
        'PATCH',
        '/apps/' + encodeURIComponent(app.name) + '/auto-update',
        { enabled: !!enabled }
      );
      if (!resp || !resp.ok) {
        app.auto_update = prev; // revert on failure
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.toast(data.error || 'Failed to update auto-update setting', 'error', 5000);
      } else {
        this.toast(
          enabled ? 'Auto-update enabled for ' + app.name : 'Auto-update disabled for ' + app.name,
          'success'
        );
      }
    } catch (e) {
      app.auto_update = prev;
      this.toast('Failed to update auto-update setting', 'error', 5000);
    } finally {
      app._autoUpdateBusy = false;
    }
  },

  async triggerUpdate(app) {
    if (!app) return;
    try {
      const resp = await this.instanceApi(
        'POST',
        '/apps/' + encodeURIComponent(app.name) + '/update'
      );
      if (resp && resp.ok) {
        const data = await resp.json().catch(() => ({}));
        const attemptID = data.attempt_id || '';
        // Pre-stamp the row so the badge shows "Updating…" before the
        // first SSE frame arrives.
        app.update = {
          ...(app.update || {}),
          stage: 'pulling',
          attempt_id: attemptID,
        };
        if (attemptID && !this.eventsByAttempt[attemptID]) {
          this.eventsByAttempt[attemptID] = [];
        }
        this.toast('Update triggered for ' + app.name, 'info', 3000);
      } else if (resp && resp.status === 409) {
        this.toast('Update already running for ' + app.name, 'warning', 4000);
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        this.toast(data.error || 'Failed to trigger update', 'error', 5000);
      }
    } catch (e) {
      this.toast('Failed to trigger update', 'error', 5000);
    }
  },

  // ---- Real-time feed (SSE) ------------------------------------------------
  // Opens a single EventSource for the currently selected instance and
  // dispatches each event to handleFeedEvent. Idempotent: re-calling while a
  // connection exists is a no-op; switching instances should call stopFeed()
  // first.
  startFeed() {
    if (typeof EventSource === 'undefined') return;
    if (this.feedConnection) return;

    // EventSource cannot send custom headers, so the JWT is passed as a
    // query parameter (same pattern as the WebSocket endpoints in
    // containers.js / templates.js — see middleware.go for the auth path).
    const instance = this.selectedInstance || 'local';
    const token = encodeURIComponent(this.token || '');
    const url = '/api/instances/' + encodeURIComponent(instance) + '/apps/updates/stream?token=' + token;

    let es;
    try {
      es = new EventSource(url);
    } catch (e) {
      return;
    }
    this.feedConnection = es;

    es.onmessage = (e) => {
      let ev;
      try { ev = JSON.parse(e.data); } catch (_) { return; }
      this.handleFeedEvent(ev);
    };

    // The browser auto-reconnects with backoff on transient errors. We log
    // and otherwise leave the connection alone; if the server closes it for
    // good (e.g., logout), stopFeed() will tear it down explicitly.
    es.onerror = () => {
      // No-op; let EventSource's built-in reconnect handle it.
    };
  },

  stopFeed() {
    if (this.feedConnection) {
      try { this.feedConnection.close(); } catch (_) {}
      this.feedConnection = null;
    }
  },

  // Apply one App_Update_Feed event to the local store.
  //
  // The event drives:
  //   - the per-app `update` field (stage, error_code, attempt_id) → table
  //     badge (R4.4)
  //   - the per-attempt event log used by the "Logs (live)" tab
  //   - one toast on the first occurrence of a terminal stage
  //     (`rolled_back`, `failed`, `completed`) for that attempt (R4.4)
  handleFeedEvent(ev) {
    if (!ev || !ev.app) return;

    const app = (this.apps || []).find(a => a.name === ev.app);
    if (app) {
      app.update = {
        ...(app.update || {}),
        attempt_id: ev.attempt_id || (app.update && app.update.attempt_id) || '',
        stage: ev.stage || (app.update && app.update.stage) || '',
        error_code: ev.error_code || '',
        message: ev.message || '',
        at: ev.at || 0,
      };
    }

    // Decide toast eligibility BEFORE pushing the event onto the per-attempt
    // log: we want to toast only on the first occurrence of a terminal stage
    // for a given attempt. If the same stage is already in the log for this
    // attempt, the duplicate is suppressed. Events without an attempt_id
    // (defensive) still toast — they cannot be deduped anyway.
    const stage = ev.stage || '';
    const isTerminal = stage === 'rolled_back' || stage === 'failed' || stage === 'completed';
    let shouldToast = isTerminal;
    if (isTerminal && ev.attempt_id) {
      const prior = this.eventsByAttempt[ev.attempt_id] || [];
      if (prior.some(p => p.stage === stage)) shouldToast = false;
    }

    if (ev.attempt_id) {
      const list = this.eventsByAttempt[ev.attempt_id] || [];
      list.push(ev);
      this.eventsByAttempt[ev.attempt_id] = list;
    }

    if (shouldToast) {
      const name = ev.app;
      if (stage === 'rolled_back') {
        this.toast(name + ': rolled back to previous version', 'warning', 6000);
      } else if (stage === 'failed') {
        const code = ev.error_code ? ' (' + ev.error_code + ')' : '';
        this.toast(name + ': update failed' + code, 'error', 6000);
      } else if (stage === 'completed') {
        this.toast(name + ': updated', 'success', 3000);
      }
    }
  },

  // ---- Helpers -------------------------------------------------------------
  // Seed the transient `update` field from the AppSummary's last_update so
  // the badge shows the correct state on first render. When the previous
  // attempt completed cleanly we treat the app as idle (the badge logic in
  // apps.html prefers `has_update` over a stale `completed` stage).
  _initialUpdateFor(app) {
    const last = app && app.last_update;
    if (!last) return { stage: 'idle', attempt_id: '' };
    const stage = last.stage || 'idle';
    return {
      stage: stage === 'completed' ? 'idle' : stage,
      attempt_id: last.attempt_id || '',
      error_code: last.error_code || '',
      message: last.message || '',
    };
  },
};
