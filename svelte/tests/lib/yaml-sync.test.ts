import { describe, it, expect } from 'vitest';
import { yamlToJson, jsonToYaml, envsubst, envsubstYAML } from '$lib/yaml-sync';

describe('yamlToJson', () => {
  it('parses services into a plain object', () => {
    const { config } = yamlToJson(`services:\n  web:\n    image: nginx:latest\n`);
    expect(config.services.web.image).toBe('nginx:latest');
  });

  it('fills in empty services when absent', () => {
    const { config } = yamlToJson(`version: "3"\n`);
    expect(config.services).toEqual({});
  });

  it('rejects services as a list', () => {
    expect(() => yamlToJson(`services: [web]\n`)).toThrow('Services must be an object');
  });

  it('throws on invalid YAML', () => {
    expect(() => yamlToJson('services:\n\tbad:')).toThrow();
  });
});

describe('jsonToYaml + comment preservation', () => {
  it('keeps comments when regenerating YAML after a GUI edit', () => {
    const original = `# top comment
services:
  web: # web service
    image: nginx:latest
    # about ports
    ports:
      - "8080:80" # host port
`;
    const { config, doc } = yamlToJson(original);
    // Simulate GUI edit
    config.services.web.restart = 'unless-stopped';
    const { yaml } = jsonToYaml(config, doc);

    expect(yaml).toContain('# top comment');
    expect(yaml).toContain('# web service');
    expect(yaml).toContain('# about ports');
    expect(yaml).toContain('# host port');
    expect(yaml).toContain('unless-stopped');
  });

  it('keeps comments when a service is removed', () => {
    const original = `services:
  a:
    image: a:1
  # b is disabled for now
  b:
    image: b:1
`;
    const { config, doc } = yamlToJson(original);
    delete config.services.b;
    const { yaml } = jsonToYaml(config, doc);
    expect(yaml).toContain('a:1');
    expect(yaml).not.toContain('b:1');
  });

  it('adds a new service without destroying existing comments', () => {
    const original = `services:
  web: # keep me
    image: nginx
`;
    const { config, doc } = yamlToJson(original);
    config.services.db = { image: 'postgres:16' };
    const { yaml } = jsonToYaml(config, doc);
    expect(yaml).toContain('# keep me');
    expect(yaml).toContain('postgres:16');
  });
});

describe('envsubst', () => {
  it('expands $VAR and ${VAR}', () => {
    expect(envsubst('img:$TAG and ${TAG}', { TAG: '1.0' })).toBe('img:1.0 and 1.0');
  });

  it('expands unknown variables to empty string', () => {
    expect(envsubst('x${MISSING}y', {})).toBe('xy');
  });

  it('leaves $$ and plain text alone', () => {
    expect(envsubst('no vars here', { A: '1' })).toBe('no vars here');
  });
});

describe('envsubstYAML', () => {
  it('substitutes inside mapping and sequence values, preserving structure', () => {
    const content = `services:
  web:
    image: nginx:$TAG
    ports:
      - "\${HTTP_PORT}:80"
`;
    const out = envsubstYAML(content, { TAG: '1.27', HTTP_PORT: '8080' });
    expect(out).toContain('nginx:1.27');
    expect(out).toContain('8080:80');
    // still valid parseable yaml afterwards
    const { config } = yamlToJson(out);
    expect(config.services.web.image).toBe('nginx:1.27');
  });

  it('does not substitute keys', () => {
    const content = `services:\n  $SVC:\n    image: nginx\n`;
    const out = envsubstYAML(content, { SVC: 'web' });
    expect(out).toContain('$SVC:');
  });
});
