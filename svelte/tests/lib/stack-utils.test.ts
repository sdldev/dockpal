import { describe, it, expect } from 'vitest';
import { parseDockerPort, parseEnvFile } from '$lib/stack-utils';

describe('parseDockerPort', () => {
  it('parses a bare port', () => {
    const r = parseDockerPort('3000', 'host');
    expect(r).toEqual({ url: 'http://host:3000', display: '3000' });
  });

  it('parses host:container mapping', () => {
    const r = parseDockerPort('8080:80', 'host');
    expect(r).toEqual({ url: 'http://host:8080', display: '8080' });
  });

  it('parses ip:host:container mapping', () => {
    const r = parseDockerPort('127.0.0.1:9000:9000', 'host');
    expect(r.url).toBe('http://127.0.0.1:9000');
    expect(r.display).toBe('127.0.0.1:9000');
  });

  it('parses docker ps arrow format', () => {
    // Dockge behavior: the ip part is discarded in favor of the hostname arg
    const r = parseDockerPort('0.0.0.0:8080->8080/tcp', 'host');
    expect(r.url).toBe('http://host:8080');
  });

  it('uses https for port 443', () => {
    const r = parseDockerPort('443:443', 'host');
    expect(r.url).toBe('https://host:443');
  });

  it('keeps udp protocol', () => {
    const r = parseDockerPort('5353:5353/udp', 'host');
    expect(r.url).toBe('udp://host:5353');
  });

  it('uses first port of a range', () => {
    const r = parseDockerPort('9090-9091:8080-8081', 'host');
    expect(r.url).toBe('http://host:9090');
  });
});

describe('parseEnvFile', () => {
  it('parses KEY=VALUE lines', () => {
    expect(parseEnvFile('A=1\nB=two\n')).toEqual({ A: '1', B: 'two' });
  });

  it('skips comments and empty lines', () => {
    expect(parseEnvFile('# hi\n\nA=1\n')).toEqual({ A: '1' });
  });

  it('strips surrounding quotes', () => {
    expect(parseEnvFile('A="quoted"\nB=\'single\'\n')).toEqual({ A: 'quoted', B: 'single' });
  });

  it('keeps = inside values', () => {
    expect(parseEnvFile('URL=postgres://u:p@h:5432/db?x=1\n')).toEqual({
      URL: 'postgres://u:p@h:5432/db?x=1'
    });
  });
});
