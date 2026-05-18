// File manager: browse container volumes.
window.Dockpal = window.Dockpal || {};

Dockpal.files = {
  async browseFiles() {
    const resp = await this.api('GET', '/api/files?container=' + this.fileManager.containerId + '&path=' + encodeURIComponent(this.fileManager.path));
    if (resp) this.fileManager.files = await resp.json();
  },

  navigateFile(f) {
    if (f.is_dir) {
      this.fileManager.path = this.fileManager.path.replace(/\/$/, '') + '/' + f.name;
      this.browseFiles();
    }
  },
};
