// Update Modal module: Shows modal dialog when new version is available.
// Integrates with version check API and handles update workflow.

window.Dockpal = window.Dockpal || {};

Dockpal.updateBanner = {
  // State properties
  updateAvailable: false,
  updateVersion: '',
  updateReleaseNotes: '',
  updateDownloadUrl: '',
  updateDismissed: false,
  updateModalVisible: false,
  updateProgress: null,  // { status, message, percentage }
  updateChecking: false,

  // Check if update is available by fetching from API
  async checkForUpdates() {
    if (!this.token) return;
    this.updateChecking = true;
    try {
      const res = await fetch('/api/system/version', {
        headers: { 'Authorization': 'Bearer ' + this.token }
      });
      if (res.ok) {
        const data = await res.json();
        this.currentVersion = (data.currentVersion || '').replace(/^v/, '');

        if (data.updateAvailable) {
          this.updateAvailable = true;
          this.updateVersion = (data.latestVersion || '').replace(/^v/, '');
          this.updateReleaseNotes = data.releaseNotes || '';
          this.updateDownloadUrl = data.downloadUrl || '';

          // Check if user skipped this specific version
          const skipped = localStorage.getItem('update_skipped_version');
          if (skipped === this.updateVersion) {
            this.updateDismissed = true;
          } else {
            this.updateDismissed = false;
            this.updateModalVisible = true;
          }
        } else {
          this.updateAvailable = false;
          this.toast('You are on the latest version (v' + this.currentVersion + ')', 'success');
        }
      }
    } catch (e) {
      this.toast('Failed to check for updates', 'error');
    } finally {
      this.updateChecking = false;
    }
  },

  // Show the update modal
  showUpdateModal() {
    this.updateModalVisible = true;
    this.updateDismissed = false;
  },

  // Close the modal without action
  closeUpdateModal() {
    this.updateModalVisible = false;
  },

  // Skip this version — won't show modal again for this version
  skipVersion() {
    localStorage.setItem('update_skipped_version', this.updateVersion);
    this.updateDismissed = true;
    this.updateModalVisible = false;
  },

  // Legacy show/hide for sidebar link
  show() {
    if (this.updateAvailable) {
      this.showUpdateModal();
    } else {
      this.checkForUpdates();
    }
  },

  hide() {
    this.closeUpdateModal();
  },

  // Handle Update Now button click
  async performUpdate() {
    if (!this.updateDownloadUrl) {
      this.toast('No update URL available', 'error');
      return;
    }

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

      if (!res.ok && !res.headers.get('content-type')?.includes('text/event-stream')) {
        const err = await res.json().catch(() => ({}));
        throw new Error(err.error || 'Update failed');
      }

      // Handle SSE stream
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });

        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        for (const line of lines) {
          if (line.startsWith('data: ')) {
            try {
              const event = JSON.parse(line.slice(6));
              this.updateProgress = {
                status: event.status || 'downloading',
                message: event.message || '',
                percentage: event.percentage || 0
              };
              if (event.status === 'complete') {
                this.toast('Update complete! Reloading...', 'success');
                setTimeout(() => window.location.reload(), 2000);
                return;
              }
              if (event.status === 'error') {
                throw new Error(event.message || 'Update failed');
              }
            } catch (parseErr) {
              if (parseErr.message && parseErr.message !== 'Update failed') continue;
              throw parseErr;
            }
          }
        }
      }
    } catch (e) {
      this.updateProgress = { status: 'error', message: e.message, percentage: 0 };
      this.toast('Update failed: ' + e.message, 'error', 8000);
    }
  },

  // Clear progress state
  clearProgress() {
    this.updateProgress = null;
  }
};