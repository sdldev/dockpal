// Update Banner module: Shows notification when new version is available.
// Integrates with version check API and handles update workflow.

window.Dockpal = window.Dockpal || {};

Dockpal.updateBanner = {
  // State properties
  updateAvailable: false,
  updateVersion: '',
  updateReleaseNotes: '',
  updateDownloadUrl: '',
  updateDismissed: false,
  updateProgress: null,  // { status, message, percentage }

  // Check if update is available by fetching from API
  async checkForUpdates() {
    if (this.token) {
      try {
        const res = await fetch('/api/system/version', {
          headers: { 'Authorization': 'Bearer ' + this.token }
        });
        if (res.ok) {
          const data = await res.json();
          if (data.updateAvailable) {
            this.updateAvailable = data.updateAvailable;
            this.updateVersion = data.latestVersion;
            this.updateReleaseNotes = data.releaseNotes || '';
            this.updateDownloadUrl = data.downloadUrl || '';
            
            // Check if user dismissed for this session
            const dismissed = localStorage.getItem('update_dismissed');
            this.updateDismissed = dismissed === 'true';
          }
        }
      } catch (e) {
        console.error('Failed to check for updates:', e);
      }
    }
  },

  // Show the update banner
  show() {
    this.updateDismissed = false;
  },

  // Hide the update banner (dismiss)
  hide() {
    this.updateAvailable = false;
    this.updateDismissed = true;
    localStorage.setItem('update_dismissed', 'true');
  },

  // Update progress during update process
  updateProgress(status, message, percentage) {
    this.updateProgress = { status, message, percentage };
  },

  // Handle Update Now button click
  async performUpdate() {
    if (!this.updateDownloadUrl) {
      this.toast('No update URL available', 'error');
      return;
    }

    // Set updating state
    this.updateProgress = { status: 'starting', message: 'Starting update...', percentage: 0 };
    this.updateProgress = { status: 'downloading', message: 'Downloading update...', percentage: 10 };

    try {
      const res = await fetch('/api/system/update', {
        method: 'POST',
        headers: {
          'Authorization': 'Bearer ' + this.token,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ downloadUrl: this.updateDownloadUrl })
      });

      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.message || 'Update failed');
      }

      const data = await res.json();
      
      // Handle progress updates
      if (data.status === 'downloading') {
        this.updateProgress('downloading', data.message || 'Downloading...', 30);
      }
      if (data.status === 'installing') {
        this.updateProgress('installing', data.message || 'Installing...', 60);
      }
      if (data.status === 'restarting') {
        this.updateProgress('restarting', data.message || 'Restarting service...', 80);
      }
      if (data.status === 'complete') {
        this.updateProgress('complete', 'Update complete! Reloading...', 100);
        this.toast('Update successful! Reloading...', 'success');
        setTimeout(() => window.location.reload(), 2000);
        return;
      }
      if (data.status === 'error') {
        throw new Error(data.message || 'Update failed');
      }
    } catch (e) {
      this.updateProgress('error', e.message, 0);
      this.toast('Update failed: ' + e.message, 'error');
    }
  },

  // Clear progress state
  clearProgress() {
    this.updateProgress = null;
  }
};