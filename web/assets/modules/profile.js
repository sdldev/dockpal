// Profile page: view user details, change password, user management (admin).
window.Dockpal = window.Dockpal || {};

Dockpal.profile = {
  async loadProfile() {
    this.profileLoading = true;
    this.changePasswordError = '';
    this.changePasswordSuccess = '';
    try {
      const resp = await this.api('GET', '/api/profile');
      if (resp && resp.ok) {
        this.profile = await resp.json();
        if (this.profile.role) this.userRole = this.profile.role;
      }
      // Load users list if admin
      if (this.userRole === 'admin') await this.loadUsers();
    } catch (e) {
      this.toast('Failed to load profile', 'error');
    } finally {
      this.profileLoading = false;
    }
  },

  async loadUsers() {
    this.usersLoading = true;
    try {
      const resp = await this.api('GET', '/api/users');
      if (resp && resp.ok) {
        this.users = await resp.json();
      }
    } catch (e) {
      this.toast('Failed to load users', 'error');
    } finally {
      this.usersLoading = false;
    }
  },

  async updateUserRole(targetUsername, newRole) {
    try {
      const resp = await this.api('PUT', '/api/users/' + targetUsername + '/role', { role: newRole });
      if (resp && resp.ok) {
        this.toast('Role updated for ' + targetUsername, 'success');
        await this.loadUsers();
      } else {
        const data = await resp.json();
        this.toast(data.error || 'Failed to update role', 'error');
        await this.loadUsers();
      }
    } catch (e) {
      this.toast('Connection error', 'error');
    }
  },

  async changePassword() {
    this.changePasswordError = '';
    this.changePasswordSuccess = '';

    if (this.changePasswordForm.new_password !== this.changePasswordForm.confirm_password) {
      this.changePasswordError = 'New passwords do not match';
      return;
    }

    if (this.changePasswordForm.new_password.length < 8) {
      this.changePasswordError = 'New password must be at least 8 characters';
      return;
    }

    try {
      const resp = await this.api('PUT', '/api/profile/password', {
        current_password: this.changePasswordForm.current_password,
        new_password: this.changePasswordForm.new_password,
      });
      const data = await resp.json();
      if (resp.ok) {
        this.changePasswordSuccess = 'Password updated successfully';
        this.changePasswordForm = { current_password: '', new_password: '', confirm_password: '' };
        this.toast('Password updated successfully', 'success');
      } else {
        this.changePasswordError = data.error || 'Failed to update password';
      }
    } catch (e) {
      this.changePasswordError = 'Connection error';
    }
  },
};
