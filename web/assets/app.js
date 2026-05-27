// Dockpal application orchestrator.
// Merges initial state with all behavior modules into a single Alpine data object.
//
// Module load order is enforced by the <script> tags in index.html:
//   state → ui → lifecycle → auth → router → charts → computed → dashboard →
//   containers → templates → services → images → domains → files → app (this file)

function dockpalApp() {
  const D = window.Dockpal;
  if (!D) throw new Error('Dockpal modules not loaded');

  // Build target with the initial state.
  const target = D.initialState();

  // Merge plain methods from each module (preserves getters via descriptors).
  const modules = [D.ui, D.lifecycle, D.auth, D.router, D.charts, D.computed, D.dashboard,
                   D.containers, D.templates, D.services, D.images,
                   D.domains, D.files, D.updateBanner, D.registry, D.instances, D.fleet, D.profile, D.imageUpdates, D.apps];
  for (const mod of modules) {
    const descriptors = Object.getOwnPropertyDescriptors(mod);
    Object.defineProperties(target, descriptors);
  }
  // Store reference for popstate handler in router.js
  window.Dockpal._app = target;
  return target;
}
