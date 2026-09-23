// Rolling-window helpers for live statistics charts (legacy UI parity).
// Legacy keeps a 30-point history with time labels and shifts on overflow.

export interface StatBuffer {
  labels: string[];
  values: number[];
}

/** Create an empty buffer. */
export function newStatBuffer(): StatBuffer {
  return { labels: [], values: [] };
}

/** Append a point with a time label; shift oldest beyond `cap` (legacy: 30). */
export function pushPoint(buf: StatBuffer, value: number, cap = 30): void {
  buf.labels.push(new Date().toLocaleTimeString('en-US', { hour12: false }));
  buf.values.push(value);
  if (buf.labels.length > cap) {
    buf.labels.shift();
    buf.values.shift();
  }
}

/** Dynamic y-axis bounds like legacy _lineChartOpts: data ±5/±10, capped at 100. */
export function dynamicYBounds(values: number[]): { min: number; max: number } {
  const maxVal = Math.max(...values, 0);
  const minVal = Math.min(...values, 0);
  return {
    min: Math.max(0, minVal - 5),
    max: Math.min(100, Math.max(10, maxVal + 10))
  };
}

/** Format bytes like legacy formatBytes (B → KB → MB → GB → TB). */
export function formatBytes(bytes: number, decimals = 1): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const idx = Math.min(i, units.length - 1);
  const value = bytes / Math.pow(k, idx);
  return `${value.toFixed(idx === 0 ? 0 : decimals)} ${units[idx]}`;
}

/** Max of a series with a floor (for network chart dynamic max). */
export function seriesMax(values: number[]): number {
  return values.length ? Math.max(...values) : 0;
}
