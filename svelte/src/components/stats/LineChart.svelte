<script module lang="ts">
  // chart.js (~200 kB minified) ships as its own lazy chunk: only chart
  // components need it, so it's imported on first render instead of weighing
  // down the entry bundle. Module scope = one shared load + registration for
  // every chart instance (Chart.register is idempotent anyway).
  let chartModulePromise: Promise<typeof import('chart.js')> | null = null;

  export function loadChart(): Promise<typeof import('chart.js')> {
    chartModulePromise ??= import('chart.js').then((m) => {
      m.Chart.register(
        m.LineController, m.LineElement, m.PointElement, m.LinearScale,
        m.CategoryScale, m.Filler, m.Tooltip, m.Legend
      );
      return m;
    });
    return chartModulePromise;
  }
</script>

<script lang="ts">
  // Chart.js 4 line chart wrapper — mirrors the legacy UI's charts.js config:
  // fill:true, tension:0.3, pointRadius:0, hidden legend, update('none').
  import { onMount, onDestroy } from 'svelte';
  import type { Chart } from 'chart.js';

  export interface ChartSeries {
    label: string;
    color: string;
    data: number[];
  }

  interface Props {
    series: ChartSeries[];
    labels: string[];
    yMin?: number;
    yMax?: number;
    formatY?: (v: number) => string;
    height?: number;
  }

  let {
    series,
    labels,
    yMin,
    yMax,
    formatY,
    height = 144
  }: Props = $props();

  let canvas: HTMLCanvasElement;
  let chart: Chart | null = null;
  // Guards against racing builds: two overlapping async builds could both see
  // `chart === null` and construct twice on the same canvas.
  let buildRun = 0;

  function defaultYFmt(v: number) { return `${Number(v).toFixed(0)}%`; }

  async function buildOrUpdate() {
    const run = ++buildRun;
    const { Chart: ChartCtor } = await loadChart();
    if (run !== buildRun || !canvas) return; // superseded or destroyed meanwhile
    // Copy labels/data into plain arrays: callers pass Svelte $state proxies,
    // and Chart.js does Object.defineProperty(data, '_chartjs', ...) in
    // listenArrayEvents(), which Svelte's state proxy rejects with
    // "state_descriptors_fixed" — leaving the canvas registered but undrawn.
    const data = {
      labels: [...labels],
      datasets: series.map((s) => ({
        label: s.label,
        data: [...s.data],
        borderColor: s.color,
        backgroundColor: s.color + '33', // ~20% alpha fill like legacy
        fill: true,
        tension: 0.3,
        pointRadius: 0,
        borderWidth: 2
      }))
    };

    const min = yMin ?? Math.min(...series.flatMap((s) => s.data), 0);
    const max = yMax ?? Math.max(...series.flatMap((s) => s.data), 1) * 1.2;

    if (chart) {
      chart.data = data;
      const y = chart.options.scales?.y as Record<string, unknown> | undefined;
      if (y) {
        y.min = min;
        y.max = max;
      }
      chart.update('none'); // no animation on tick (legacy parity)
      return;
    }

    chart = new ChartCtor(canvas, {
      type: 'line',
      data,
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        plugins: {
          legend: { display: series.length > 1 },
          tooltip: { enabled: true }
        },
        scales: {
          x: {
            ticks: { display: false },
            grid: { display: false }
          },
          y: {
            min,
            max,
            ticks: { callback: (v) => (formatY ?? defaultYFmt)(Number(v)) }
          }
        }
      }
    });
  }

  onMount(() => void buildOrUpdate());

  $effect(() => {
    // Re-run when any reactive input changes
    void labels.length;
    void series.map((s) => [s.label, s.data.length]);
    if (canvas) void buildOrUpdate();
  });

  onDestroy(() => {
    buildRun++; // invalidate any in-flight build
    chart?.destroy();
    chart = null;
  });
</script>

<div class="relative w-full" style="height: {height}px">
  <canvas bind:this={canvas}></canvas>
</div>
