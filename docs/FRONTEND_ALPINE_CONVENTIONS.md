# Dockpal Frontend Alpine Conventions

Dockpal intentionally keeps the current frontend stack lightweight: HTML
partials, Tailwind utility classes, and Alpine-compatible modules under
`web/assets/modules`. Use these conventions before considering a heavier SPA
rewrite.

## Module shape

- Every module attaches to `window.Dockpal`.
- Keep state in `web/assets/modules/state.js`.
- Keep behavior in a domain module, for example:
  - `containers.js`
  - `images.js`
  - `apps.js`
  - `registry.js`
- `web/assets/app.js` merges modules into the single Alpine data object.

```js
window.Dockpal = window.Dockpal || {};

Dockpal.example = {
  async loadExample() {
    const resp = await this.instanceApi('GET', '/example');
    if (resp && resp.ok) this.example = await resp.json();
  },
};
```

## API calls

- Use `this.api(method, absoluteApiPath, body)` for global routes.
- Use `this.instanceApi(method, instanceRelativePath, body)` for Docker-host
  scoped routes.
- Check `resp && resp.ok` before parsing success data.
- Parse error bodies with `.catch(() => ({}))` so bad responses do not break UI.

```js
const resp = await this.instanceApi('POST', '/containers/' + id + '/restart');
if (!resp || !resp.ok) {
  const data = resp ? await resp.json().catch(() => ({})) : {};
  this.toast(data.error || 'Request failed', 'error', 5000);
  return;
}
```

## Long-lived resources

Store long-lived handles on state and clean them through `lifecycle.js`:

- `containerLogSocket`
- `templateDeploySocket`
- `installLogSocket`
- `statsInterval`
- `sysResourceInterval`
- `fleetInterval`
- App update `EventSource` via `stopFeed()`

Before opening a new WebSocket or interval, close the previous one.

```js
if (this.closeWebSocket) this.closeWebSocket('containerLogSocket');
this.containerLogSocket = new WebSocket(url);
```

On logout or instance/page teardown, use:

```js
this.cleanupSessionResources();
this.cleanupContainerDetail();
```

## Page partials

- Keep Alpine expressions short.
- Move business logic into modules when an expression grows beyond simple
  property access or one method call.
- Prefer helper methods like `selectedContainerUpdateAvailable()` over repeating
  nested optional chains in HTML.

## UX guidance

- Be explicit for destructive or non-obvious Docker behavior.
- For image updates, tell users that restart does not pull newer tags and that
  running containers need recreation to use a pulled image.
- Prefer the Apps page for managed compose projects because it has health check
  and rollback semantics.

## Validation

There is no separate frontend build step today. After frontend changes:

1. Run backend tests because the web assets are embedded/served by Go:
   `go test ./...`
2. Load the UI in browser and check the touched page.
3. Watch browser console for Alpine expression errors.
