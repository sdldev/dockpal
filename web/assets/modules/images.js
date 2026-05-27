// Images: list, pull, remove.
window.Dockpal = window.Dockpal || {};

Dockpal.images = {
  async loadImages() {
    // Use instanceApi for instance-scoped image listing
    const resp = await this.instanceApi('GET', '/images');
    if (resp) {
      this.images = await resp.json();
      this.imageCount = this.images.length;
    }
    // Also load cached update status
    await this.loadUpdateStatus();
  },

  async pullImage() {
    const imageRef = (this.imagePullName || '').trim();
    if (!imageRef) {
      this.toast('Enter an image name to pull', 'warning', 3000);
      return;
    }
    this.toast('Pulling ' + imageRef + '... This only downloads the image; existing containers will not change until recreated.', 'info', 6000);
    // Use instanceApi for image pull
    const resp = await this.instanceApi('POST', '/images/pull', { image: imageRef });
    if (resp && resp.ok) {
      this.toast('Image pulled: ' + imageRef + '. Recreate containers to use the new image.', 'success', 6000);
    } else {
      const data = resp ? await resp.json().catch(() => ({})) : {};
      this.toast(data.error || ('Pull failed for ' + imageRef), 'error', 6000);
    }
    this.imagePullName = '';
    await this.loadImages();
  },

  removeImage(id) {
    const img = this.images.find(i => i.id === id);
    this.showConfirm({
      title: 'Remove Image',
      message: 'Remove image "' + (img ? img.repo + ':' + img.tag : id.slice(0, 12)) + '"? Containers using this image must be removed first.',
      confirmText: 'Remove',
      onConfirm: async () => {
        // Use instanceApi for image removal
        const resp = await this.instanceApi('DELETE', '/images/' + id);
        if (resp && !resp.ok) {
          const data = await resp.json().catch(() => ({}));
          this.toast(data.error || 'Failed to remove image', 'error', 5000);
        } else {
          this.toast('Image removed', 'success');
        }
        await this.loadImages();
      }
    });
  },

  async pruneImages() {
    this.showConfirm({
      title: 'Prune Unused Images',
      message: 'Remove all unused images? This includes:\n\u2022 Dangling images (<none>:<none>)\n\u2022 Images not referenced by any container\n\nThis action cannot be undone.',
      confirmText: 'Prune Images',
      danger: true,
      onConfirm: async () => {
        this.imagePruning = true;
        const resp = await this.instanceApi('POST', '/images/prune', { dangling_only: false });
        if (resp && resp.ok) {
          const result = await resp.json();
          const reclaimed = this.formatBytes ? this.formatBytes(result.space_reclaimed) : (result.space_reclaimed + ' B');
          this.toast('Pruned ' + result.images_deleted + ' image(s), reclaimed ' + reclaimed, 'success', 6000);
        } else {
          const data = resp ? await resp.json().catch(() => ({})) : {};
          this.toast(data.error || 'Prune failed', 'error', 5000);
        }
        this.imagePruning = false;
        await this.loadImages();
      }
    });
  },
};
