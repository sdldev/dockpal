#!/usr/bin/env node
// Generates the dockpal template catalog (templates/*.json).
// Ports the Doktainer app-store catalog into dockpal's JSON format,
// with compose YAML authored for dockpal's compose-native pipeline.
const fs = require('fs');
const path = require('path');

const OUT_DIR = path.resolve(__dirname, '..', 'templates');

// ---- compose helpers -------------------------------------------------------
function indent(lines, n) {
  const pad = '  '.repeat(n);
  return lines.map((l) => (l ? pad + l : l));
}

function buildCompose(spec) {
  const lines = ['services:'];
  for (const s of spec.services) {
    lines.push(`  ${s.name}:`);
    if (s.image) lines.push(`    image: ${s.image}`);
    if (s.command) lines.push(`    command: ${s.command}`);
    if (s.capAdd && s.capAdd.length) {
      lines.push('    cap_add:');
      for (const c of s.capAdd) lines.push(`      - ${c}`);
    }
    if (s.sysctls) lines.push('    sysctls:');
    for (const k in s.sysctls) lines.push(`      ${k}: ${s.sysctls[k]}`);
    if (s.dependsOn && s.dependsOn.length) {
      lines.push('    depends_on:');
      for (const d of s.dependsOn) lines.push(`      - ${d}`);
    }
    const env = s.env || {};
    const envKeys = Object.keys(env);
    if (envKeys.length) {
      lines.push('    environment:');
      for (const k of envKeys) {
        const v = env[k];
        lines.push(`      ${k}: ${quoteScalar(v)}`);
      }
    }
    if (s.ports && s.ports.length) {
      lines.push('    ports:');
      for (const p of s.ports) lines.push(`      - '${p}'`);
    }
    if (s.volumes && s.volumes.length) {
      lines.push('    volumes:');
      for (const v of s.volumes) lines.push(`      - ${v}`);
    }
  }
  const vols = new Set();
  for (const s of spec.services) for (const v of s.volumes || []) {
    if (!v.includes(':') && !v.startsWith('/')) vols.add(v);
  }
  if (vols.size) {
    lines.push('volumes:');
    for (const v of vols) lines.push(`  ${v}:`);
  }
  return lines.join('\n');
}

function quoteScalar(v) {
  if (v === undefined || v === null) return '';
  const s = String(v);
  if (/^(true|false|yes|no|null|\d+)$/i.test(s) || s.startsWith('${')) return s;
  if (/[:#\[\]{},&*!|>'"]|\s/.test(s)) return `"${s.replace(/"/g, '\\"')}"`;
  return s;
}

// ---- catalog ---------------------------------------------------------------
// ports: [label, hostDefault, containerPort]
const T = [];

function add(spec) {
  const ports = (spec.ports || []).map(([label, def, cport]) => ({ label, default: def, container_port: cport }));
  T.push({
    id: spec.id,
    name: spec.name,
    description: spec.desc,
    category: spec.category,
    icon: spec.icon,
    icon_url: spec.iconUrl,
    tags: spec.tags || [],
    popular: !!spec.popular,
    env_required: spec.envRequired || [],
    ports,
    compose: buildCompose(spec)
  });
}

// --- databases --------------------------------------------------------------
add({ id: 'postgres16', name: 'PostgreSQL 17', desc: 'Advanced relational database', category: 'database', icon: '🐘', iconUrl: 'https://cdn.simpleicons.org/postgresql/336791', tags: ['sql', 'relational'], popular: true, envRequired: ['POSTGRES_PASSWORD', 'POSTGRES_DB'], ports: [['PostgreSQL', 5432, 5432]], services: [{ name: 'postgres', image: 'postgres:17-alpine', env: { POSTGRES_PASSWORD: '${POSTGRES_PASSWORD}', POSTGRES_DB: '${POSTGRES_DB}' }, ports: ['5432:5432'], volumes: ['pg-data:/var/lib/postgresql/data'] }] });
add({ id: 'mariadb', name: 'MariaDB 11.4', desc: 'MySQL-compatible database', category: 'database', icon: '🦭', iconUrl: 'https://cdn.simpleicons.org/mariadb/003545', tags: ['sql', 'mysql-compatible'], popular: true, envRequired: ['MARIADB_ROOT_PASSWORD', 'MARIADB_DATABASE'], ports: [['MariaDB', 3306, 3306]], services: [{ name: 'mariadb', image: 'mariadb:11.4', env: { MARIADB_ROOT_PASSWORD: '${MARIADB_ROOT_PASSWORD}', MARIADB_DATABASE: '${MARIADB_DATABASE}' }, ports: ['3306:3306'], volumes: ['mariadb-data:/var/lib/mysql'] }] });
add({ id: 'mysql8', name: 'MySQL 8.4', desc: 'Popular relational database', category: 'database', icon: '🐬', iconUrl: 'https://cdn.simpleicons.org/mysql/4479A1', tags: ['sql', 'relational'], popular: true, envRequired: ['MYSQL_ROOT_PASSWORD', 'MYSQL_DATABASE'], ports: [['MySQL', 3307, 3306]], services: [{ name: 'mysql', image: 'mysql:8.4', env: { MYSQL_ROOT_PASSWORD: '${MYSQL_ROOT_PASSWORD}', MYSQL_DATABASE: '${MYSQL_DATABASE}' }, ports: ['3307:3306'], volumes: ['mysql-data:/var/lib/mysql'] }] });
add({ id: 'mongo7', name: 'MongoDB 7', desc: 'Document-oriented NoSQL database', category: 'database', icon: '🍃', iconUrl: 'https://cdn.simpleicons.org/mongodb/47A248', tags: ['nosql', 'document'], popular: false, envRequired: ['MONGO_INITDB_ROOT_PASSWORD'], ports: [['MongoDB', 27018, 27017]], services: [{ name: 'mongo', image: 'mongo:7', env: { MONGO_INITDB_ROOT_USERNAME: 'root', MONGO_INITDB_ROOT_PASSWORD: '${MONGO_INITDB_ROOT_PASSWORD}' }, ports: ['27018:27017'], volumes: ['mongo-data:/data/db'] }] });
add({ id: 'clickhouse', name: 'ClickHouse', desc: 'Column-oriented analytics database', category: 'database', icon: '🛢️', iconUrl: 'https://cdn.simpleicons.org/clickhouse/FFCC01', tags: ['analytics', 'columnar'], popular: false, envRequired: ['CLICKHOUSE_PASSWORD'], ports: [['HTTP', 8123, 8123]], services: [{ name: 'clickhouse', image: 'clickhouse/clickhouse-server:24.10', env: { CLICKHOUSE_PASSWORD: '${CLICKHOUSE_PASSWORD}', CLICKHOUSE_DB: 'default' }, ports: ['8123:8123'], volumes: ['clickhouse-data:/var/lib/clickhouse'] }] });

// --- cache ------------------------------------------------------------------
add({ id: 'redis', name: 'Redis 7.4', desc: 'In-memory data store & cache', category: 'cache', icon: '⚡', iconUrl: 'https://cdn.simpleicons.org/redis/DC382D', tags: ['kv', 'cache'], popular: true, ports: [['Redis', 6379, 6379]], services: [{ name: 'redis', image: 'redis:7.4-alpine', ports: ['6379:6379'], volumes: ['redis-data:/data'] }] });
add({ id: 'memcached', name: 'Memcached 1.6', desc: 'Distributed memory object caching', category: 'cache', icon: '🧩', iconUrl: 'https://cdn.simpleicons.org/memcached/000000', tags: ['kv', 'cache'], popular: false, ports: [['Memcached', 11212, 11211]], services: [{ name: 'memcached', image: 'memcached:1.6-alpine', ports: ['11212:11211'] }] });

// --- web servers ------------------------------------------------------------
add({ id: 'nginx', name: 'Nginx', desc: 'High-performance web server & reverse proxy', category: 'web-server', icon: '🌐', iconUrl: 'https://cdn.simpleicons.org/nginx/009639', tags: ['http', 'proxy'], popular: true, ports: [['HTTP', 8081, 80]], services: [{ name: 'nginx', image: 'nginx:1.27-alpine', ports: ['8081:80'], volumes: ['nginx-html:/usr/share/nginx/html'] }] });
add({ id: 'apache', name: 'Apache HTTP Server', desc: 'Classic open-source web server', category: 'web-server', icon: '🐍', iconUrl: 'https://cdn.simpleicons.org/apache/D22128', tags: ['http'], popular: false, ports: [['HTTP', 8083, 80]], services: [{ name: 'apache', image: 'httpd:2.4-alpine', ports: ['8083:80'] }] });
add({ id: 'caddy', name: 'Caddy 2', desc: 'Web server with automatic HTTPS', category: 'web-server', icon: '🟩', iconUrl: 'https://cdn.simpleicons.org/caddy/1F6FEB', tags: ['http', 'tls'], popular: false, ports: [['HTTP', 8090, 80], ['HTTPS', 8443, 443]], services: [{ name: 'caddy', image: 'caddy:2', ports: ['8090:80', '8443:443'], volumes: ['caddy-data:/data', 'caddy-config:/config'] }] });

// --- cms --------------------------------------------------------------------
add({ id: 'wordpress', name: 'WordPress', desc: 'Popular CMS for websites and blogs', category: 'cms', icon: '📝', iconUrl: 'https://cdn.simpleicons.org/wordpress/21759B', tags: ['blog', 'php'], popular: true, envRequired: ['WORDPRESS_DB_PASSWORD'], ports: [['Web', 8084, 80]], services: [
  { name: 'wordpress', image: 'wordpress:6.7-php8.3-apache', env: { WORDPRESS_DB_HOST: 'db', WORDPRESS_DB_USER: 'wordpress', WORDPRESS_DB_PASSWORD: '${WORDPRESS_DB_PASSWORD}', WORDPRESS_DB_NAME: 'wordpress' }, ports: ['8084:80'], volumes: ['wp-data:/var/www/html'], dependsOn: ['db'] },
  { name: 'db', image: 'mariadb:11.4', env: { MARIADB_DATABASE: 'wordpress', MARIADB_USER: 'wordpress', MARIADB_PASSWORD: '${WORDPRESS_DB_PASSWORD}', MARIADB_ROOT_PASSWORD: '${WORDPRESS_DB_PASSWORD}' }, volumes: ['wp-db:/var/lib/mysql'] }
] });
add({ id: 'ghost', name: 'Ghost', desc: 'Professional publishing platform', category: 'cms', icon: '👻', iconUrl: 'https://cdn.simpleicons.org/ghost/738A94', tags: ['blog', 'node'], popular: false, envRequired: ['GHOST_URL'], ports: [['Web', 8085, 2368]], services: [{ name: 'ghost', image: 'ghost:5', env: { url: '${GHOST_URL}' }, ports: ['8085:2368'], volumes: ['ghost-data:/var/lib/ghost/content'] }] });
add({ id: 'directus', name: 'Directus', desc: 'Headless CMS & data platform', category: 'cms', icon: '🔲', iconUrl: 'https://cdn.simpleicons.org/directus/64F', tags: ['headless', 'api'], popular: false, envRequired: ['SECRET', 'ADMIN_EMAIL', 'ADMIN_PASSWORD'], ports: [['Web', 8056, 8055]], services: [{ name: 'directus', image: 'directus/directus:11', env: { SECRET: '${SECRET}', ADMIN_EMAIL: '${ADMIN_EMAIL}', ADMIN_PASSWORD: '${ADMIN_PASSWORD}', DB_CLIENT: 'sqlite3' }, ports: ['8056:8055'], volumes: ['directus-data:/directus/database'] }] });

// --- proxy ------------------------------------------------------------------
add({ id: 'traefik', name: 'Traefik', desc: 'Cloud-native reverse proxy & load balancer', category: 'proxy', icon: '🛡️', iconUrl: 'https://cdn.simpleicons.org/traefikproxy/24A1C1', tags: ['proxy', 'load-balancer'], popular: true, ports: [['Dashboard', 8091, 8080], ['HTTP', 8092, 80]], services: [{ name: 'traefik', image: 'traefik:v3.1', command: '--api.insecure=true --providers.docker=true --providers.docker.exposedbydefault=false', ports: ['8091:8080', '8092:80'], volumes: ['/var/run/docker.sock:/var/run/docker.sock:ro'] }] });
add({ id: 'nginx-proxy-manager', name: 'Nginx Proxy Manager', desc: 'Reverse proxy with easy SSL management', category: 'proxy', icon: '🔐', iconUrl: 'https://cdn.simpleicons.org/nginxproxymanager/F15833', tags: ['proxy', 'ssl'], popular: true, ports: [['Admin', 8093, 81], ['HTTP', 8094, 80], ['HTTPS', 8444, 443]], services: [{ name: 'npm', image: 'jc21/nginx-proxy-manager:latest', ports: ['8093:81', '8094:80', '8444:443'], volumes: ['npm-data:/data', 'npm-ssl:/etc/letsencrypt'] }] });

// --- monitoring -------------------------------------------------------------
add({ id: 'grafana', name: 'Grafana', desc: 'Monitoring & observability platform', category: 'monitoring', icon: '📊', iconUrl: 'https://cdn.simpleicons.org/grafana/F46800', tags: ['dashboards', 'metrics'], popular: true, envRequired: ['GF_SECURITY_ADMIN_PASSWORD'], ports: [['Grafana Web', 3000, 3000]], services: [{ name: 'grafana', image: 'grafana/grafana:11.5.2', env: { GF_SECURITY_ADMIN_PASSWORD: '${GF_SECURITY_ADMIN_PASSWORD}', GF_USERS_ALLOW_SIGN_UP: false }, ports: ['3000:3000'], volumes: ['grafana-data:/var/lib/grafana'] }] });
add({ id: 'prometheus', name: 'Prometheus', desc: 'Open-source metrics & alerting toolkit', category: 'monitoring', icon: '📈', iconUrl: 'https://cdn.simpleicons.org/prometheus/E6522C', tags: ['metrics', 'scrape'], popular: false, ports: [['Prometheus', 9091, 9090]], services: [{ name: 'prometheus', image: 'prom/prometheus:v2.54.1', ports: ['9091:9090'], volumes: ['prometheus-data:/prometheus'] }] });
add({ id: 'uptime-kuma', name: 'Uptime Kuma', desc: 'Self-hosted uptime monitoring', category: 'monitoring', icon: '📡', iconUrl: 'https://cdn.simpleicons.org/uptimekuma/5CDD8B', tags: ['uptime', 'status'], popular: true, ports: [['Web', 3002, 3001]], services: [{ name: 'uptime-kuma', image: 'louislam/uptime-kuma:1', ports: ['3002:3001'], volumes: ['uptime-kuma-data:/app/data'] }] });
add({ id: 'netdata', name: 'Netdata', desc: 'Real-time system & container monitoring', category: 'monitoring', icon: '🖥️', iconUrl: 'https://cdn.simpleicons.org/netdata/00D9FF', tags: ['real-time', 'metrics'], popular: false, ports: [['Web', 19999, 19999]], services: [{ name: 'netdata', image: 'netdata/netdata:latest', ports: ['19999:19999'] }] });
add({ id: 'portainer', name: 'Portainer CE', desc: 'Container management UI', category: 'monitoring', icon: '🐳', iconUrl: 'https://cdn.simpleicons.org/portainer/13BEF9', tags: ['containers', 'admin'], popular: true, ports: [['Web', 9444, 9443]], services: [{ name: 'portainer', image: 'portainer/portainer-ce:2.21.4', ports: ['9444:9443'], volumes: ['portainer-data:/data', '/var/run/docker.sock:/var/run/docker.sock'] }] });
add({ id: 'dozzle', name: 'Dozzle', desc: 'Real-time container log viewer', category: 'monitoring', icon: '📜', iconUrl: 'https://cdn.simpleicons.org/dozzle/4DABF7', tags: ['logs', 'containers'], popular: false, ports: [['Web', 8086, 8080]], services: [{ name: 'dozzle', image: 'amir20/dozzle:latest', ports: ['8086:8080'], volumes: ['/var/run/docker.sock:/var/run/docker.sock:ro'] }] });

// --- storage ----------------------------------------------------------------
add({ id: 'minio', name: 'MinIO', desc: 'S3-compatible object storage', category: 'storage', icon: '🗄️', iconUrl: 'https://cdn.simpleicons.org/minio/CD2C24', tags: ['s3', 'object-storage'], popular: true, envRequired: ['MINIO_ROOT_PASSWORD'], ports: [['API', 9000, 9000], ['Console', 9002, 9001]], services: [{ name: 'minio', image: 'minio/minio:latest', command: 'server /data --console-address :9001', env: { MINIO_ROOT_USER: 'minioadmin', MINIO_ROOT_PASSWORD: '${MINIO_ROOT_PASSWORD}' }, ports: ['9000:9000', '9002:9001'], volumes: ['minio-data:/data'] }] });
add({ id: 'nextcloud', name: 'Nextcloud', desc: 'Self-hosted file sync & share', category: 'storage', icon: '☁️', iconUrl: 'https://cdn.simpleicons.org/nextcloud/0082C9', tags: ['files', 'sync'], popular: true, ports: [['Web', 8087, 80]], services: [{ name: 'nextcloud', image: 'nextcloud:30-apache', ports: ['8087:80'], volumes: ['nextcloud-data:/var/www/html'] }] });
add({ id: 'filebrowser', name: 'File Browser', desc: 'Web-based file manager', category: 'storage', icon: '📁', iconUrl: 'https://cdn.simpleicons.org/filebrowser/41A4FF', tags: ['files', 'manager'], popular: false, ports: [['Web', 8088, 80]], services: [{ name: 'filebrowser', image: 'filebrowser/filebrowser:latest', ports: ['8088:80'], volumes: ['filebrowser-data:/database', 'filebrowser-files:/srv'] }] });

// --- devtools ---------------------------------------------------------------
add({ id: 'adminer', name: 'Adminer', desc: 'Database management UI', category: 'devtools', icon: '🛠️', iconUrl: 'https://cdn.simpleicons.org/adminer/34567C', tags: ['sql', 'admin'], popular: false, ports: [['Web', 8082, 8080]], services: [{ name: 'adminer', image: 'adminer:latest', ports: ['8082:8080'] }] });
add({ id: 'gitea', name: 'Gitea', desc: 'Lightweight self-hosted Git service', category: 'devtools', icon: '🍵', iconUrl: 'https://cdn.simpleicons.org/gitea/609926', tags: ['git', 'scm'], popular: true, ports: [['Web', 3003, 3000], ['SSH', 2223, 22]], services: [{ name: 'gitea', image: 'gitea/gitea:1.22', ports: ['3003:3000', '2223:22'], volumes: ['gitea-data:/data'] }] });
add({ id: 'jenkins', name: 'Jenkins', desc: 'Automation server for CI/CD', category: 'devtools', icon: '🧰', iconUrl: 'https://cdn.simpleicons.org/jenkins/D24939', tags: ['ci', 'cd'], popular: false, ports: [['Web', 8089, 8080], ['Agents', 50001, 50000]], services: [{ name: 'jenkins', image: 'jenkins/jenkins:lts', ports: ['8089:8080', '50001:50000'], volumes: ['jenkins-data:/var/jenkins_home'] }] });
add({ id: 'code-server', name: 'VS Code Server', desc: 'Code editor in your browser', category: 'devtools', icon: '💻', iconUrl: 'https://cdn.simpleicons.org/codeigniter/DE5285', tags: ['ide', 'code'], popular: true, envRequired: ['PASSWORD'], ports: [['Web', 8445, 8080]], services: [{ name: 'code-server', image: 'codercom/code-server:latest', env: { PASSWORD: '${PASSWORD}' }, ports: ['8445:8080'], volumes: ['code-server-data:/home/coder/.local/share/code-server'] }] });
add({ id: 'dockge', name: 'Dockge', desc: 'Manage compose stacks with style', category: 'devtools', icon: '🧱', iconUrl: 'https://cdn.simpleicons.org/dockge/4CC0E2', tags: ['compose', 'stacks'], popular: false, ports: [['Web', 5002, 5001]], services: [{ name: 'dockge', image: 'louislam/dockge:1', ports: ['5002:5001'], volumes: ['dockge-data:/app/data', '/var/run/docker.sock:/var/run/docker.sock', 'dockge-stacks:/opt/stacks'] }] });

// --- automation / messaging -------------------------------------------------
add({ id: 'n8n', name: 'n8n', desc: 'Workflow automation platform', category: 'automation', icon: '🔀', iconUrl: 'https://cdn.simpleicons.org/n8n/EA4B71', tags: ['workflow', 'automation'], popular: true, ports: [['Web', 5679, 5678]], services: [{ name: 'n8n', image: 'n8nio/n8n:latest', env: { GENERIC_TIMEZONE: 'UTC' }, ports: ['5679:5678'], volumes: ['n8n-data:/home/node/.n8n'] }] });
add({ id: 'node-red', name: 'Node-RED', desc: 'Flow-based programming for IoT & automation', category: 'automation', icon: '🔴', iconUrl: 'https://cdn.simpleicons.org/nodered/8F0000', tags: ['iot', 'flows'], popular: false, ports: [['Web', 1881, 1880]], services: [{ name: 'node-red', image: 'nodered/node-red:latest', ports: ['1881:1880'], volumes: ['nodered-data:/data'] }] });
add({ id: 'rabbitmq', name: 'RabbitMQ', desc: 'Message broker with management UI', category: 'messaging', icon: '🐇', iconUrl: 'https://cdn.simpleicons.org/rabbitmq/FF6600', tags: ['queue', 'amqp'], popular: false, ports: [['AMQP', 5673, 5672], ['Management', 15673, 15672]], services: [{ name: 'rabbitmq', image: 'rabbitmq:3.13-management', ports: ['5673:5672', '15673:15672'], volumes: ['rabbitmq-data:/var/lib/rabbitmq'] }] });

// --- ai ---------------------------------------------------------------------
add({ id: 'ollama', name: 'Ollama', desc: 'Run large language models locally', category: 'ai', icon: '🤖', iconUrl: 'https://cdn.simpleicons.org/ollama/000000', tags: ['llm', 'ml'], popular: true, ports: [['API', 11435, 11434]], services: [{ name: 'ollama', image: 'ollama/ollama:latest', ports: ['11435:11434'], volumes: ['ollama-data:/root/.ollama'] }] });
add({ id: 'open-webui', name: 'Open WebUI', desc: 'Chat interface for Ollama LLMs', category: 'ai', icon: '💬', iconUrl: 'https://cdn.simpleicons.org/openai/412991', tags: ['chat', 'llm'], popular: true, ports: [['Web', 3004, 8080]], services: [{ name: 'open-webui', image: 'ghcr.io/open-webui/open-webui:main', ports: ['3004:8080'], volumes: ['open-webui-data:/app/backend/data'] }] });

// --- media ------------------------------------------------------------------
add({ id: 'jellyfin', name: 'Jellyfin', desc: 'Self-hosted media server', category: 'media', icon: '🎬', iconUrl: 'https://cdn.simpleicons.org/jellyfin/00A4DC', tags: ['media', 'streaming'], popular: false, ports: [['Web', 8097, 8096]], services: [{ name: 'jellyfin', image: 'jellyfin/jellyfin:10.10', ports: ['8097:8096'], volumes: ['jellyfin-config:/config', 'jellyfin-cache:/cache', 'jellyfin-media:/media'] }] });
add({ id: 'audiobookshelf', name: 'Audiobookshelf', desc: 'Self-hosted audiobook & podcast server', category: 'media', icon: '🎧', iconUrl: 'https://cdn.simpleicons.org/audiobookshelf/82612C', tags: ['audiobooks', 'podcasts'], popular: false, ports: [['Web', 13378, 80]], services: [{ name: 'audiobookshelf', image: 'advplyr/audiobookshelf:latest', ports: ['13378:80'], volumes: ['abs-audio:/audiobooks', 'abs-podcasts:/podcasts', 'abs-config:/config', 'abs-meta:/metadata'] }] });

// --- network ----------------------------------------------------------------
add({ id: 'cloudflared', name: 'Cloudflare Tunnel', desc: 'Expose services via Cloudflare', category: 'network', icon: '🌩️', iconUrl: 'https://cdn.simpleicons.org/cloudflare/F38020', tags: ['tunnel', 'dns'], popular: false, envRequired: ['TUNNEL_TOKEN'], services: [{ name: 'cloudflared', image: 'cloudflare/cloudflared:latest', command: 'tunnel run --token ${TUNNEL_TOKEN}' }] });
add({ id: 'wireguard-ui', name: 'WireGuard UI', desc: 'Web UI for managing WireGuard peers', category: 'network', icon: '🔒', iconUrl: 'https://cdn.simpleicons.org/wireguard/88171A', tags: ['vpn', 'wireguard'], popular: false, ports: [['Web', 5050, 5000]], services: [{ name: 'wireguard-ui', image: 'ngoduykhanh/wireguard-ui:latest', capAdd: ['NET_ADMIN'], ports: ['5050:5000'], volumes: ['wgui-db:/app/db', 'wgui-config:/app/config'] }] });

// write files
fs.mkdirSync(OUT_DIR, { recursive: true });
for (const t of T) {
  fs.writeFileSync(path.join(OUT_DIR, t.id + '.json'), JSON.stringify(t, null, 2) + '\n');
}
console.log(`Wrote ${T.length} templates to ${OUT_DIR}`);