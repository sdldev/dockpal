// Chart.js helpers. Chart instances stored outside Alpine reactive system.
window.Dockpal = window.Dockpal || {};
Dockpal._charts = { cpu: null, ram: null, cpuChart: null, memChart: null, netChart: null };

Dockpal.charts = {
  // System dashboard charts (CPU + RAM, stacked)
  renderSysResourceChart() {
    this._renderSysChart('cpuChart', 'cpu', '#3b82f6', '#3b82f615');
    this._renderSysChart('ramChart', 'ram', '#10b981', '#10b98115');
  },

  _renderSysChart(canvasId, dataKey, color, bgColor) {
    const canvas = document.getElementById(canvasId);
    if (!canvas) return;
    const chartKey = canvasId === 'cpuChart' ? 'cpu' : 'ram';
    const data = this.sysResourceHistory[dataKey];
    const minVal = Math.max(0, Math.floor(Math.min(...data) - 5));
    const maxVal = Math.min(100, Math.ceil(Math.max(...data) + 10));

    if (Dockpal._charts[chartKey]) {
      Dockpal._charts[chartKey].data.labels = [...this.sysResourceHistory.labels];
      Dockpal._charts[chartKey].data.datasets[0].data = [...data];
      Dockpal._charts[chartKey].options.scales.y.min = minVal;
      Dockpal._charts[chartKey].options.scales.y.max = maxVal;
      Dockpal._charts[chartKey].update('none');
      return;
    }

    Dockpal._charts[chartKey] = new Chart(canvas, {
      type: 'line',
      data: {
        labels: [...this.sysResourceHistory.labels],
        datasets: [{
          data: [...data], borderColor: color, backgroundColor: bgColor,
          borderWidth: 2, tension: 0.3, fill: true, pointRadius: 0,
          pointHoverRadius: 4, pointHoverBackgroundColor: color, spanGaps: true
        }]
      },
      options: this._lineChartOpts(minVal, maxVal, v => v + '%')
    });
  },

  // Container detail page charts (CPU, Memory, Network)
  renderContainerCharts() {
    this._renderContainerChart('containerCpuChart', 'cpuChart', this.statsHistory.cpu, '#3b82f6', '#3b82f615', { max: 100, fmt: v => v.toFixed(1) + '%' });
    this._renderContainerChart('containerMemChart', 'memChart', this.statsHistory.mem, '#10b981', '#10b98115', { max: 100, fmt: v => v.toFixed(1) + '%' });
    this._renderNetChart();
  },

  _renderContainerChart(canvasId, chartKey, data, color, bgColor, opts) {
    const canvas = document.getElementById(canvasId);
    if (!canvas) return;
    const labels = this.statsHistory.labels;

    if (Dockpal._charts[chartKey]) {
      Dockpal._charts[chartKey].data.labels = [...labels];
      Dockpal._charts[chartKey].data.datasets[0].data = [...data];
      Dockpal._charts[chartKey].update('none');
      return;
    }

    Dockpal._charts[chartKey] = new Chart(canvas, {
      type: 'line',
      data: {
        labels: [...labels],
        datasets: [{
          data: [...data], borderColor: color, backgroundColor: bgColor,
          borderWidth: 2, tension: 0.3, fill: true, pointRadius: 0, pointHoverRadius: 3
        }]
      },
      options: this._lineChartOpts(0, opts.max, opts.fmt)
    });
  },

  _renderNetChart() {
    const canvas = document.getElementById('containerNetChart');
    if (!canvas) return;
    const fmtBytes = (v) => this.formatBytes(v);
    const labels = this.statsHistory.labels;
    const rxData = this.statsHistory.rx;
    const txData = this.statsHistory.tx;
    const maxVal = Math.max(...rxData, ...txData, 1);

    if (Dockpal._charts.netChart) {
      Dockpal._charts.netChart.data.labels = [...labels];
      Dockpal._charts.netChart.data.datasets[0].data = [...rxData];
      Dockpal._charts.netChart.data.datasets[1].data = [...txData];
      Dockpal._charts.netChart.options.scales.y.max = maxVal * 1.2;
      Dockpal._charts.netChart.update('none');
      return;
    }

    Dockpal._charts.netChart = new Chart(canvas, {
      type: 'line',
      data: {
        labels: [...labels],
        datasets: [
          { label: 'RX', data: [...rxData], borderColor: '#a78bfa', backgroundColor: '#a78bfa15', borderWidth: 2, tension: 0.3, fill: true, pointRadius: 0, pointHoverRadius: 3 },
          { label: 'TX', data: [...txData], borderColor: '#f472b6', backgroundColor: '#f472b615', borderWidth: 2, tension: 0.3, fill: true, pointRadius: 0, pointHoverRadius: 3 }
        ]
      },
      options: {
        responsive: true, maintainAspectRatio: false, animation: { duration: 400 },
        interaction: { intersect: false, mode: 'index' },
        scales: {
          y: { beginAtZero: true, max: maxVal * 1.2, ticks: { color: '#52525b', font: { size: 9 }, callback: v => fmtBytes(v) }, grid: { color: '#27272a40' }, border: { display: false } },
          x: { ticks: { color: '#52525b', font: { size: 9 }, maxTicksLimit: 6, maxRotation: 0 }, grid: { display: false }, border: { display: false } }
        },
        plugins: {
          legend: { display: false },
          tooltip: { backgroundColor: '#18181b', borderColor: '#3f3f46', borderWidth: 1, titleColor: '#f4f4f5', bodyColor: '#d4d4d8', padding: 8, cornerRadius: 6, callbacks: { label: ctx => ctx.dataset.label + ': ' + fmtBytes(ctx.parsed.y) } }
        }
      }
    });
  },

  _lineChartOpts(min, max, fmt) {
    return {
      responsive: true, maintainAspectRatio: false, animation: { duration: 400 },
      interaction: { intersect: false, mode: 'index' },
      scales: {
        y: { min, max, beginAtZero: min === 0, ticks: { color: '#52525b', font: { size: 9 }, callback: v => fmt ? fmt(v) : v }, grid: { color: '#27272a40' }, border: { display: false } },
        x: { ticks: { color: '#52525b', font: { size: 9 }, maxTicksLimit: 6, maxRotation: 0 }, grid: { display: false }, border: { display: false } }
      },
      plugins: {
        legend: { display: false },
        tooltip: { backgroundColor: '#18181b', borderColor: '#3f3f46', borderWidth: 1, titleColor: '#f4f4f5', bodyColor: '#d4d4d8', padding: 8, cornerRadius: 6, callbacks: { label: ctx => fmt ? fmt(ctx.parsed.y) : ctx.parsed.y } }
      }
    };
  },

  // Backwards-compatibility alias
  renderChart() { this.renderContainerCharts(); },

  destroyChart() {
    ['cpuChart', 'memChart', 'netChart'].forEach(k => {
      if (Dockpal._charts[k]) { Dockpal._charts[k].destroy(); Dockpal._charts[k] = null; }
    });
    if (this.statsInterval) { clearInterval(this.statsInterval); this.statsInterval = null; }
  },
};
