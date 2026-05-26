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
  async checkImageUpdate(imageRef, opts = {}) {
    const silent = opts.silent === true;
    if (!imageRef) return null;
    this.imageUpdateChecking = true;
    if (!silent) this.toast('Checking update for ' + imageRef + '...', 'info', 2000);
    try {
      const resp = await this.instanceApi('POST', '/images/check', { image: imageRef });
      if (resp && resp.ok) {
        const result = await resp.json();
        if (result.has_update && !silent) {
          this.toast('Update available for ' + imageRef, 'info', 5000);
        } else if (result.error && !silent) {
          this.toast('Check failed: ' + result.error, 'warning', 4000);
        } else if (!silent) {
          this.toast(imageRef + ' is up to date', 'success', 2000);
        }
        // Refresh cached status
        await this.loadUpdateStatus();
        return result;
      } else {
        const data = resp ? await resp.json().catch(() => ({})) : {};
        if (!silent) this.toast(data.error || 'Check failed', 'error', 5000);
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
    this.toast('Pulling latest for ' + imageRef + '... This downloads the image only; containers keep their current image until recreated.', 'info', 7000);
    try {
      const resp = await this.instanceApi('POST', '/images/pull-force', { image: imageRef });
      if (resp && resp.ok) {
        this.toast('Latest image pulled: ' + imageRef + '. Recreate containers to run it.', 'success', 7000);
        await this.loadImages();
        await this.loadUpdateStatus();
      } else {
        const data = await resp.json().catch(() => ({}));
        this.toast(data.error || ('Pull latest failed for ' + imageRef), 'error', 6000);
      }
    } catch (e) {
      this.toast('Pull latest failed for ' + imageRef, 'error', 6000);
    }
  },

  async checkSelectedContainerImageUpdate() {
    if (!this.selectedContainer?.image) return;
    const imageRef = this.selectedContainer.image;
    this.containerImageUpdateChecking = true;
    this.containerImageUpdateResult = null;
    this.toast('Checking latest image for ' + imageRef + '...', 'info', 2500);
    try {
      const result = await this.checkImageUpdate(imageRef, { silent: true });
      this.containerImageUpdateResult = result || null;
      if (result?.has_update) {
        this.toast('Update available for ' + imageRef + '. Use Pull latest & recreate to apply it.', 'info', 7000);
      } else if (result?.error) {
        this.toast('Image check failed: ' + result.error, 'warning', 5000);
      } else if (result) {
        this.toast(imageRef + ' is up to date', 'success', 3000);
      }
    } finally {
      this.containerImageUpdateChecking = false;
    }
  },

  async updateSelectedContainerImage() {
    if (!this.selectedContainer?.id) return;
    const containerId = this.selectedContainer.id;
    const imageRef = this.selectedContainer.image || 'image';
    this.showConfirm({
      title: 'Pull latest & recreate',
      message: 'Dockpal will pull the latest image for ' + imageRef + ' and recreate this container. Restart alone does not pull or apply newer images.',
      confirmText: 'Pull & Recreate',
      danger: false,
      onConfirm: async () => {
        this.containerImageUpdating = true;
        this.toast('Pulling latest image and recreating container...', 'info', 7000);
        try {
          const resp = await this.instanceApi('POST', '/containers/' + containerId + '/update-image');
          if (resp && resp.ok) {
            this.toast('Container recreated with latest image', 'success', 7000);
            await this.loadDashboard();
            await this.loadUpdateStatus();
            await this.refreshContainerDetailNow(containerId);
          } else {
            const data = resp ? await resp.json().catch(() => ({})) : {};
            this.toast(data.error || 'Failed to pull latest and recreate container', 'error', 7000);
          }
        } catch (e) {
          this.toast('Failed to pull latest and recreate container', 'error', 7000);
        } finally {
          this.containerImageUpdating = false;
        }
      }
    });
  },

  // Get update status for a specific image
  getImageUpdateStatus(imageRef) {
    if (!imageRef) return null;
    return this.imageUpdates.find(u => u.image_ref === imageRef) || null;
  },

  // Computed: count of images with available updates
  get imagesWithUpdatesCount() {
    return this.imageUpdates.filter(u => u.result && u.result.has_update).length;
  }
};
