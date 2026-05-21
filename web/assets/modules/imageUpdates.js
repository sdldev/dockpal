// Image update checking and notification module.
window.Dockpal = window.Dockpal || {};

Dockpal.imageUpdates = {
  // Load cached update statuses from the server
  async loadUpdateStatus() {
    try {
      const resp = await this.instanceApi('GET', '/images/updates');
      if (resp && resp.ok) {
        const data = await resp.json();
        this.imageUpdates = data.updates || [];
      }
    } catch (e) {
      console.error('Failed to load image update status:', e);
    }
  },

  // Check a specific image for updates
  async checkImageUpdate(imageRef) {
    this.imageUpdateChecking = true;
    this.toast('Checking update for ' + imageRef + '...', 'info', 2000);
    try {
      const resp = await this.instanceApi('POST', '/images/check', { image: imageRef });
      if (resp && resp.ok) {
        const result = await resp.json();
        if (result.has_update) {
          this.toast('Update available for ' + imageRef, 'info', 5000);
        } else if (result.error) {
          this.toast('Check failed: ' + result.error, 'warning', 4000);
        } else {
          this.toast(imageRef + ' is up to date', 'success', 2000);
        }
        // Refresh cached status
        await this.loadUpdateStatus();
        return result;
      } else {
        const data = await resp.json().catch(() => ({}));
        this.toast(data.error || 'Check failed', 'error', 5000);
      }
    } catch (e) {
      this.toast('Failed to check update', 'error', 5000);
    } finally {
      this.imageUpdateChecking = false;
    }
  },

  // Check all images for updates (manual trigger)
  async checkAllImageUpdates() {
    this.imageUpdateChecking = true;
    this.toast('Checking all images for updates...', 'info', 3000);
    try {
      // For each image, trigger a check
      const images = this.images || [];
      let checked = 0;
      let updatesFound = 0;
      for (const img of images) {
        const imageRef = img.repo + ':' + img.tag;
        if (img.repo === '<none>' || img.tag === '<none>') continue;
        const resp = await this.instanceApi('POST', '/images/check', { image: imageRef });
        if (resp && resp.ok) {
          const result = await resp.json();
          if (result.has_update) updatesFound++;
        }
        checked++;
      }
      await this.loadUpdateStatus();
      if (updatesFound > 0) {
        this.toast(updatesFound + ' update(s) available', 'info', 5000);
      } else {
        this.toast('All ' + checked + ' images are up to date', 'success', 3000);
      }
    } catch (e) {
      this.toast('Failed to check updates', 'error', 5000);
    } finally {
      this.imageUpdateChecking = false;
    }
  },

  // Force pull a specific image
  async forcePullImage(imageRef) {
    this.toast('Force pulling ' + imageRef + '...', 'info', 3000);
    try {
      const resp = await this.instanceApi('POST', '/images/pull-force', { image: imageRef });
      if (resp && resp.ok) {
        this.toast('Image pulled: ' + imageRef, 'success');
        await this.loadImages();
        await this.loadUpdateStatus();
      } else {
        const data = await resp.json().catch(() => ({}));
        this.toast(data.error || 'Force pull failed', 'error', 5000);
      }
    } catch (e) {
      this.toast('Force pull failed', 'error', 5000);
    }
  },

  // Get update status for a specific image
  getImageUpdateStatus(imageRef) {
    return this.imageUpdates.find(u => u.image_ref === imageRef);
  },

  // Computed: count of images with available updates
  get imagesWithUpdatesCount() {
    return this.imageUpdates.filter(u => u.result && u.result.has_update).length;
  }
};
