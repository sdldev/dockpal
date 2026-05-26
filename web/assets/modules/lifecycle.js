// Lifecycle helpers for Alpine modules.
//
// Keep cleanup here so page transitions, instance switches, and logout do not
// leave polling intervals, WebSockets, charts, or SSE connections running in
// the background. Modules should store long-lived handles on state.js and call
// these helpers instead of duplicating teardown logic.
window.Dockpal = window.Dockpal || {};

Dockpal.lifecycle = {
  closeWebSocket(key) {
    const ws = this[key];
    if (!ws) return;
    this[key] = null;
    try { ws.close(); } catch (_) {}
  },

  clearTimer(key) {
    if (!this[key]) return;
    clearInterval(this[key]);
    this[key] = null;
  },

  closeContainerLogStream() {
    this.closeWebSocket('containerLogSocket');
  },

  cleanupContainerDetail() {
    this.closeContainerLogStream();
    if (this.destroyChart) this.destroyChart();
    this.containerStats = null;
    this.statsHistory = { cpu: [], mem: [], rx: [], tx: [], labels: [] };
    this.logs = [];
  },

  cleanupDeployStreams() {
    this.closeWebSocket('templateDeploySocket');
    this.closeWebSocket('installLogSocket');
  },

  cleanupPolling() {
    this.clearTimer('statsInterval');
    this.clearTimer('sysResourceInterval');
    this.clearTimer('fleetInterval');
  },

  cleanupSessionResources() {
    this.cleanupContainerDetail();
    this.cleanupDeployStreams();
    this.cleanupPolling();
    if (this.stopFeed) this.stopFeed();
    if (window.Dockpal && Dockpal._charts) {
      ['cpu', 'ram', 'cpuChart', 'memChart', 'netChart'].forEach((key) => {
        if (Dockpal._charts[key]) {
          try { Dockpal._charts[key].destroy(); } catch (_) {}
          Dockpal._charts[key] = null;
        }
      });
    }
  },
};