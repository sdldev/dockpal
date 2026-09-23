<script lang="ts">
  // Chart.js 4 line chart wrapper — mirrors the legacy UI's charts.js config:
  // fill:true, tension:0.3, pointRadius:0, hidden legend, update('none').
  import { onMount, onDestroy } from 'svelte';
  import {
    Chart, LineController, LineElement, PointElement, LinearScale,
    CategoryScale, Filler, Tooltip, Legend
  } from 'chart.js';

  Chart.register(LineController, LineElement, PointElement, LinearScale, CategoryScale, Filler, Tooltip, Legend);

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

  function defaultYFmt(v: number) { return `${Number(v).toFixed(0)}%`; }

  function buildOrUpdate() {
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

    chart = new Chart(canvas, {
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

  onMount(() => buildOrUpdate());

  $effect(() => {
    // Re-run when any reactive input changes
    void labels.length;
    void series.map((s) => [s.label, s.data.length]);
    if (canvas) buildOrUpdate();
  });

  onDestroy(() => {
    chart?.destroy();
    chart = null;
  });
</script>

<div class="relative w-full" style="height: {height}px">
  <canvas bind:this={canvas}></canvas>
</div>
