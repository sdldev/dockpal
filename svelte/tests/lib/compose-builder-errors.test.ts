import { describe, expect, it } from 'vitest';
import { collectDeployErrors, SERVICE_NAME_RE } from '$lib/compose-builder';

const validCustom = {
  mode: 'custom' as const,
  serviceName: 'web',
  template: null,
  env: {},
  ports: {},
  customImage: 'nginx:alpine',
  customEnv: [],
  customPorts: [{ host: 8080, container: 80, protocol: 'tcp' as const }]
};

describe('collectDeployErrors', () => {
  it('accepts a valid custom form', () => {
    expect(collectDeployErrors(validCustom)).toEqual({});
  });

  it('rejects a bad service name before it reaches the backend', () => {
    const errors = collectDeployErrors({ ...validCustom, serviceName: 'bad name!' });
    expect(errors.serviceName).toContain('letters, digits');
  });

  it('requires template env vars and sane host ports', () => {
    const errors = collectDeployErrors({
      mode: 'template',
      serviceName: 'web',
      template: { env_required: ['API_KEY'], ports: [{ container_port: 80 }] },
      env: {},
      ports: { '80': 70000 },
      customImage: '',
      customEnv: [],
      customPorts: []
    });
    expect(errors['env-API_KEY']).toBe('API_KEY is required');
    expect(errors['port-80']).toBe('Host port must be between 1 and 65535');
  });

  it('flags custom rows with missing values or out-of-range ports', () => {
    const errors = collectDeployErrors({
      ...validCustom,
      customEnv: [{ key: 'K', value: '' }],
      customPorts: [{ host: 70000, container: 80, protocol: 'tcp' }]
    });
    expect(errors['cenv-0']).toBe('Value required for K');
    expect(errors['cport-0']).toBe('Ports must be between 1 and 65535');
  });

  it('service name regex matches the backend container-name rules', () => {
    expect(SERVICE_NAME_RE.test('web-1_server.db')).toBe(true);
    expect(SERVICE_NAME_RE.test('-leading')).toBe(false);
  });
});
