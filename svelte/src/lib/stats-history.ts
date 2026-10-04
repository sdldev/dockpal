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

/** Max of a series with a floor (for network chart dynamic max). */
export function seriesMax(values: number[]): number {
  return values.length ? Math.max(...values) : 0;
}
