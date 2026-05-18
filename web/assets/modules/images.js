// Images: list, pull, remove.
window.Dockpal = window.Dockpal || {};

Dockpal.images = {
  async loadImages() {
    const resp = await this.api('GET', '/api/images');
    if (resp) {
      this.images = await resp.json();
      this.imageCount = this.images.length;
    }
  },

  async pullImage() {
    this.toast('Pulling ' + this.imagePullName + '...', 'info', 2500);
    const resp = await this.api('POST', '/api/images/pull', { image: this.imagePullName });
    if (resp && resp.ok) {
      this.toast('Image pulled', 'success');
    } else {
      const data = resp ? await resp.json().catch(() => ({})) : {};
      this.toast(data.error || 'Pull failed', 'error', 5000);
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
        const resp = await this.api('DELETE', '/api/images/' + id);
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
};
