# Phase 5: Build Pipeline & Production Deployment

## Overview
Establish CI/CD pipeline, bundle Svelte for Go embed, create production-ready deployment configurations, and implement monitoring.

## Timeline
**Duration:** 2-3 days
**Priority:** High

---

## Task List

### 5.1 Build Script Integration

#### Create `.gitignore` updates

```ini
# Node dependencies (never commit)
svelte/node_modules/
svelte/.svelte-kit/
svelte/dist/

# IDE
.svelte-kit/

# OS files
.DS_Store
Thumbs.db

# Environment variables
.env
.env.local
*.env

# Build artifacts
web/.*
!/web/index.html
!/web/assets/
!/web/pages/
!/web/partials/
```

#### Update Makefile

```makefile
# Existing rules...
build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o dockpal .

dev:
	$(MAKE) build && ./dockpal server

dev-watch:
	reflex -s '*.go' -- make dev

test:
	go test -v ./...

lint:
	go vet ./...

clean:
	rm -rf dockpal dockpal-linux-amd64 .data/ coverage.out

# New Svelte-related targets
svelte-dev:
	cd svelte && npm run dev

svelte-build:
	cd svelte && npm run build

svelte-lint:
	cd svelte && npm run lint

svelte-test:
	cd svelte && npm run test:unit

# Combined dev (both frontend and backend)
dev-all:
	@echo "Starting backend..."
	@$(MAKE) dev &
	@echo "Starting frontend..."
	@cd svelte && npm run dev &
	@echo "Both services running"
	@sleep 2
	@wait

# Production build with embedded Svelte
prod-build: svelte-build build
	@echo "Production build complete: dockpal"

# Run all tests
test-all: test svelte-lint svelte-test

.PHONY: build dev dev-watch test lint clean \
        svelte-dev svelte-build svelte-lint svelte-test \
        dev-all prod-build test-all
```

---

### 5.2 GitHub Actions Workflow

#### Create `.github/workflows/ci.yml`

```yaml
name: CI

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

env:
  GO_VERSION: '1.25'
  NODE_VERSION: '20'

jobs:
  # Backend checks
  backend:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
      
      - name: Cache Go modules
        uses: actions/cache@v4
        with:
          path: ~/go/pkg/mod
          key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            ${{ runner.os }}-go-
      
      - name: Install dependencies
        run: go mod download
      
      - name: Build
        run: make build
      
      - name: Lint
        run: make lint
      
      - name: Test
        run: make test
      
      - name: Cover
        run: |
          go test -coverprofile=coverage.out ./...
          go tool cover -html=coverage.out -o coverage.html
      
      - name: Upload coverage
        uses: codecov/codecov-action@v4
        if: success()
        with:
          file: ./coverage.out
          flags: backend

  # Frontend checks
  frontend:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Node.js
        uses: actions/setup-node@v4
        with:
          node-version: ${{ env.NODE_VERSION }}
          cache: 'npm'
          cache-dependency-path: svelte/package-lock.json
      
      - name: Install dependencies
        working-directory: ./svelte
        run: npm ci
      
      - name: Lint
        working-directory: ./svelte
        run: npm run lint
      
      - name: Build
        working-directory: ./svelte
        run: npm run build
      
      - name: Type check
        working-directory: ./svelte
        run: npx svelte-check --tsconfig ./tsconfig.json
      
      - name: Unit tests
        working-directory: ./svelte
        run: npm run test:unit

  # E2E tests (optional, runs on main branch only)
  e2e:
    runs-on: ubuntu-latest
    needs: [backend, frontend]
    if: github.ref == 'refs/heads/main'
    steps:
      - uses: actions/checkout@v4
      
      - name: Start backend
        run: |
          cd .. && make build && ./dockpal server &
          sleep 5
      
      - name: Set up Node.js
        uses: actions/setup-node@v4
        with:
          node-version: ${{ env.NODE_VERSION }}
          cache: 'npm'
          cache-dependency-path: svelte/package-lock.json
      
      - name: Install dependencies
        working-directory: ./svelte
        run: npm ci
      
      - name: Playwright install
        run: cd svelte && npx playwright install --with-deps
      
      - name: Run E2E tests
        working-directory: ./svelte
        run: npm run test:e2e
        timeout-minutes: 15

  # Deploy (main branch only)
  deploy:
    runs-on: ubuntu-latest
    needs: [backend, frontend, e2e]
    if: github.ref == 'refs/heads/main'
    environment: production
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
      
      - name: Set up Node.js
        uses: actions/setup-node@v4
        with:
          node-version: ${{ env.NODE_VERSION }}
          cache: 'npm'
      
      - name: Build frontend
        run: |
          cd svelte && npm ci && npm run build
      
      - name: Build binary
        run: |
          VERSION=${{ github.sha }} make build
          cp dockpal /tmp/dockpal-bin
      
      - name: Build Linux AMD64 artifact
        run: |
          go build -ldflags "-s -w" -o dockpal-linux-amd64 \
            -tags linux,amd64 \
            -trimpath \
            .
          sha256sum dockpal-linux-amd64 > dockpal-linux-amd64.sha256
      
      - name: Upload binaries
        uses: actions/upload-artifact@v4
        with:
          name: dockpal-binaries
          path: |
            dockpal-linux-amd64
            dockpal-linux-amd64.sha256
          retention-days: 30

  # Docker image build (optional)
  docker:
    runs-on: ubuntu-latest
    needs: deploy
    if: github.ref == 'refs/heads/main'
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3
      
      - name: Login to GitHub Container Registry
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      
      - name: Build and push Docker image
        uses: docker/build-push-action@v5
        with:
          context: .
          platforms: linux/amd64
          push: true
          tags: |
            ghcr.io/${{ github.repository_owner }}/dockpal:latest
            ghcr.io/${{ github.repository_owner }}/dockpal:${{ github.sha }}
          labels: |
            org.opencontainers.image.source=${{ github.event.repository.url }}
            org.opencontainers.image.revision=${{ github.sha }}
```

---

### 5.3 Docker Compose for Development

#### Create `docker-compose.dev.yml`

```yaml
version: '3.8'

services:
  # Backend (Go + Svelte built into binary)
  dockpal:
    build:
      context: .
      dockerfile: Dockerfile.dev
    container_name: dockpal
    ports:
      - "3012:3012"
    volumes:
      - ./dockpal:/app
      - ./web:/app/web
      - dockpal-data:/opt/dockpal/data
    environment:
      - DOCKPAL_DATA_DIR=/opt/dockpal/data
      - DOCKPAL_INITIAL_ADMIN_PASSWORD=docker-compose-admin-password
      - PORT=3012
      - REFLEX_ENABLED=true
    restart: unless-stopped

  # Optional: PostgreSQL for local development
  postgres:
    image: postgres:17-alpine
    ports:
      - "5432:5432"
    environment:
      - POSTGRES_DB=dockpal_dev
      - POSTGRES_USER=devuser
      - POSTGRES_PASSWORD=devpassword
    volumes:
      - pg-data:/var/lib/postgresql/data

volumes:
  dockpal-data:
  pg-data:
```

#### Create `Dockerfile.dev`

```dockerfile
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy Go modules first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with development mode
ENV REFLEX_ENABLED=true
RUN go build -o dockpal .

FROM alpine:latest

WORKDIR /opt/dockpal

# Copy binary from builder
COPY --from=builder /app/dockpal /opt/dockpal/

# Create data directory
RUN mkdir -p /opt/dockpal/data

EXPOSE 3012

ENTRYPOINT ["/opt/dockpal/dockpal"]
CMD ["server"]
```

---

### 5.4 Production Deployment Scripts

#### Create `deployment/systemd/dockpal.service`

```ini
[Unit]
Description=Dockpal - Self-hosted Docker Management Panel
Documentation=https://github.com/sdldev/dockpal
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
User=dockpal
Group=dockpal
Environment="DOCKPAL_DATA_DIR=/opt/dockpal/data"
Environment="PORT=3012"
ExecStart=/usr/local/bin/dockpal server
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
SecurityOptions=
NoNewPrivileges=false

# Resource limits (adjust based on your VPS specs)
MemoryMax=512M
CPUQuota=150%

[Install]
WantedBy=multi-user.target
```

#### Create `deployment/scripts/install-dockpal.sh`

```bash
#!/bin/bash
set -euo pipefail

# Dockpal Installer (Debian/Ubuntu)
REPO="sdldev/dockpal"
TARGET="/usr/local/bin/dockpal"
SERVICE_NAME="dockpal"

echo "📦 Installing Dockpal..."

# Check prerequisites
command -v curl >/dev/null || { echo "curl required"; exit 1; }
command -v tar >/dev/null || { echo "tar required"; exit 1; }

# Determine architecture
ARCH=$(uname -m)
if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="amd64"
elif [[ "$ARCH" == "aarch64" ]]; then
  ARCH="arm64"
else
  echo "Unsupported architecture: $ARCH"
  exit 1
fi

# Download latest binary
echo "Downloading latest release for $ARCH..."
URL="https://github.com/$REPO/releases/latest/download/dockpal-$ARCH"
sha_url="${URL}.sha256"

TMP_BIN=$(mktemp)
if ! curl -fsSL "$URL" -o "$TMP_BIN"; then
  echo "Failed to download binary"
  rm -f "$TMP_BIN"
  exit 1
fi

# Verify checksum
if command -v sha256sum >/dev/null && curl -fsSL "$sha_url" -o "${TMP_BIN}.sha256" 2>/dev/null; then
  EXPECTED=$(awk '{print $1}' "${TMP_BIN}.sha256")
  ACTUAL=$(sha256sum "$TMP_BIN" | awk '{print $1}')
  if [[ "$EXPECTED" != "$ACTUAL" ]]; then
    echo "Checksum mismatch"
    rm -f "$TMP_BIN" "${TMP_BIN}.sha256"
    exit 1
  fi
  rm -f "${TMP_BIN}.sha256"
fi

# Install binary
mkdir -p "$(dirname "$TARGET")"
mv "$TMP_BIN" "$TARGET"
chmod +x "$TARGET"

# Create system user
if ! id -u dockpal >/dev/null 2>&1; then
  echo "Creating dockpal user..."
  useradd -r -s /bin/false dockpal
fi

# Create data directories
DATA_DIR="/opt/dockpal/data"
mkdir -p "$DATA_DIR"
chown -R dockpal:dockpal "$DATA_DIR"

# Install systemd service
cat > "/etc/systemd/system/${SERVICE_NAME}.service" << EOF
[Unit]
Description=Dockpal - Self-hosted Docker Management Panel
After=network.target docker.service

[Service]
Type=simple
User=dockpal
Group=dockpal
Environment="DOCKPAL_DATA_DIR=$DATA_DIR"
Environment="PORT=3012"
ExecStart=$TARGET server
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl start "$SERVICE_NAME"

echo ""
echo "✅ Dockpal installed successfully!"
echo ""
echo "Next steps:"
echo "1. Check status: systemctl status dockpal"
echo "2. View logs: journalctl -u dockpal -f"
echo "3. Get admin password: journalctl -u dockpal | grep admin password"
echo ""
```

---

### 5.5 Monitoring & Observability

#### Add health check endpoint

Create `src/lib/api/health.ts`:

```typescript
export async function getHealthStatus(): Promise<{
  healthy: boolean;
  version: string;
  uptime: number;
}> {
  const response = await fetch('/api/health');
  
  if (!response.ok) {
    throw new Error('Health check failed');
  }

  const data = await response.json();

  return {
    healthy: data.status === 'healthy',
    version: data.version,
    uptime: data.uptime_seconds
  };
}

// Health check interval
export function startHealthMonitoring(intervalMs: number = 60000) {
  setInterval(async () => {
    try {
      const health = await getHealthStatus();
      if (!health.healthy) {
        console.warn('Dockpal health check failed!');
      }
    } catch (error) {
      console.error('Health monitor error:', error);
    }
  }, intervalMs);
}
```

#### Dashboard health widget component

Create `src/components/Dashboard/HealthWidget.svelte`:

```svelte
<script lang="ts">
  import { onMount } from 'svelte';
  import { getHealthStatus, startHealthMonitoring } from '$lib/api/health';

  let healthy = true;
  let version = '';
  let uptime = 0;
  let loading = true;

  async function loadHealth() {
    try {
      const data = await getHealthStatus();
      healthy = data.healthy;
      version = data.version;
      uptime = data.uptime;
    } catch {
      healthy = false;
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    loadHealth();
    startHealthMonitoring(60000); // Poll every minute
  });

  const uptimeFormatted = () => {
    const hours = Math.floor(uptime / 3600);
    const minutes = Math.floor((uptime % 3600) / 60);
    return `${hours}h ${minutes}m`;
  };
</script>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 flex items-center justify-between">
  <div>
    <h3 class="text-sm font-medium text-white mb-1">System Status</h3>
    {#if healthy}
      <span class="text-xs text-emerald-400">✓ Healthy • Version {version}</span>
      <div class="text-xs text-zinc-500 mt-1">Uptime {uptimeFormatted()}</div>
    {:else}
      <span class="text-xs text-red-400">✗ Unhealthy</span>
    {/if}
  </div>

  <div class="w-4 h-4 rounded-full {healthy ? 'bg-emerald-400' : 'bg-red-400'} animate-pulse"></div>
</div>
```

---

## Deliverables Checklist

- [ ] ✅ Makefile extended with Svelte build targets
- [ ] ✅ GitHub Actions CI workflow configured
- [ ] ✅ Docker Compose for local development
- [ ] ✅ Systemd service configuration
- [ ] ✅ Production installation script
- [ ] ✅ Health monitoring integration
- [ ] ✅ Documentation updated for deployment

---

## Migration Complete

Congratulations! You've completed the full SvelteKit migration of Dockpal. The application now has:

1. ✅ Modern TypeScript-first frontend architecture
2. ✅ Component-based UI with reusable patterns
3. ✅ Real-time WebSocket streaming
4. ✅ Automated CI/CD pipeline
5. ✅ Production-ready deployment scripts
6. ✅ Health monitoring and observability

**Total estimated timeline:** 2-3 weeks of focused development

---

## Next Steps

1. Start with **Phase 1** implementation
2. Commit changes after each phase completion
3. Run full test suite before next phase
4. Document any issues or improvements in Phase 6 retrospective
