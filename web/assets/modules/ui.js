// UI helpers: confirm dialogs, toasts, clipboard, formatters.
window.Dockpal = window.Dockpal || {};

Dockpal.ui = {
  showConfirm(opts) {
    this.confirmDialog = {
      show: true,
      title: opts.title || 'Confirm',
      message: opts.message || 'Are you sure?',
      confirmText: opts.confirmText || 'Delete',
      danger: opts.danger !== false,
      onConfirm: opts.onConfirm || null
    };
  },

  async runConfirm() {
    const fn = this.confirmDialog.onConfirm;
    this.confirmDialog.show = false;
    if (typeof fn === 'function') await fn();
  },

  toast(message, type = 'info', duration = 3500) {
    const id = Date.now() + Math.random();
    this.toasts.push({ id, message, type });
    setTimeout(() => {
      this.toasts = this.toasts.filter(t => t.id !== id);
    }, duration);
  },

  copyToClipboard(text) {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text);
    } else {
      const ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
    }
  },

  formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  },
};
