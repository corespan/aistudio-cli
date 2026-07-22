(function () {
  'use strict';

  // ----- State -----
  const state = {
    runs: [],
    filteredRuns: [],
    selectedRun: null,
    selectedKey: null,
    chart: null,
    searchQuery: '',
  };

  // ----- DOM References -----
  const $ = (sel) => document.querySelector(sel);

  const dom = {
    sidebarList: $('#sidebar-run-list'),
    runCountBadge: $('#run-count-badge'),
    dashboard: $('#dashboard'),
    emptyState: $('#empty-state'),
    filterModel: $('#filter-model'),
    filterGpu: $('#filter-gpu'),
    filterPrecision: $('#filter-precision'),
    filterMetric: $('#filter-metric'),
    tableBody: $('#runs-table-body'),
    tableSearch: $('#table-search'),
    chartCanvas: $('#perf-chart'),
    configBar: $('#config-bar'),
    cfgTp: $('#cfg-tp'),
    cfgPp: $('#cfg-pp'),
    cfgQuant: $('#cfg-quant'),
    cfgDtype: $('#cfg-dtype'),
    cfgMaxlen: $('#cfg-maxlen'),
    cfgGmem: $('#cfg-gmem'),
    valTotalThroughput: $('#val-total-throughput'),
    valOutputThroughput: $('#val-output-throughput'),
    valTtft: $('#val-ttft'),
    valTpot: $('#val-tpot'),
    valVerdict: $('#val-verdict'),
  };

  // ----- API -----
  async function fetchRuns() {
    const resp = await fetch('/api/v1/runs');
    if (!resp.ok) throw new Error(`Failed to fetch runs: ${resp.status}`);
    const data = await resp.json();
    if (!Array.isArray(data)) throw new Error('Unexpected response format from /api/v1/runs');
    return data;
  }

  async function fetchRunDetail(model, gpu, timestamp, signal) {
    const resp = await fetch(
      `/api/v1/runs/${encodeURIComponent(model)}/${encodeURIComponent(gpu)}/${encodeURIComponent(timestamp)}`,
      { signal }
    );
    if (!resp.ok) throw new Error(`Failed to fetch run detail: ${resp.status}`);
    return resp.json();
  }

  // ----- Init -----
  async function init() {
    try {
      state.runs = await fetchRuns();
    } catch (err) {
      console.error('Error loading runs:', err);
      showError('Failed to load benchmark runs. Is the server running?');
      state.runs = [];
    }

    if (state.runs.length === 0) {
      dom.dashboard.classList.add('hidden');
      dom.emptyState.classList.remove('hidden');
      dom.runCountBadge.textContent = '0 runs';
      return;
    }

    dom.dashboard.classList.remove('hidden');
    dom.emptyState.classList.add('hidden');

    populateFilters();
    applyFilters();

    if (state.filteredRuns.length > 0) {
      const latest = state.filteredRuns[0];
      await selectRun(latest.model, latest.gpu, latest.timestamp);
    }

    dom.filterModel.addEventListener('change', onFilterChange);
    dom.filterGpu.addEventListener('change', onFilterChange);
    dom.filterPrecision.addEventListener('change', onFilterChange);
    dom.filterMetric.addEventListener('change', onMetricChange);
    dom.tableSearch.addEventListener('input', onTableSearch);
  }

  function precisionLabel(run) {
    const mc = run.summary && run.summary.model_config;
    if (!mc) return '';
    if (mc.quantization) return mc.quantization.toUpperCase();
    if (mc.dtype && mc.dtype !== 'auto') return mc.dtype.toUpperCase();
    return 'DEFAULT';
  }

  // Colour used in config-badge for known precisions
  const PRECISION_COLOURS = {
    FP4: { bg: 'hsla(280,70%,55%,0.18)', fg: 'hsl(280,70%,70%)', border: 'hsla(280,70%,55%,0.4)' },
    FP8: { bg: 'hsla(200,80%,50%,0.18)', fg: 'hsl(200,80%,65%)', border: 'hsla(200,80%,50%,0.4)' },
    INT8: { bg: 'hsla(38,90%,50%,0.18)', fg: 'hsl(38,90%,60%)', border: 'hsla(38,90%,50%,0.4)' },
    INT4: { bg: 'hsla(0,72%,50%,0.18)', fg: 'hsl(0,72%,65%)', border: 'hsla(0,72%,50%,0.4)' },
    BFLOAT16: { bg: 'hsla(174,72%,46%,0.15)', fg: 'hsl(174,72%,56%)', border: 'hsla(174,72%,46%,0.3)' },
    BF16: { bg: 'hsla(174,72%,46%,0.15)', fg: 'hsl(174,72%,56%)', border: 'hsla(174,72%,46%,0.3)' },
    FLOAT16: { bg: 'hsla(142,60%,40%,0.18)', fg: 'hsl(142,60%,55%)', border: 'hsla(142,60%,40%,0.4)' },
    FP16: { bg: 'hsla(142,60%,40%,0.18)', fg: 'hsl(142,60%,55%)', border: 'hsla(142,60%,40%,0.4)' },
  };

  function applyPrecisionStyle(el, label) {
    const key = label.toUpperCase().replace(/[^A-Z0-9]/g, '');
    const col = PRECISION_COLOURS[key];
    if (col) {
      el.style.background = col.bg;
      el.style.color = col.fg;
      el.style.borderColor = col.border;
    } else {
      // reset to default accent
      el.style.background = '';
      el.style.color = '';
      el.style.borderColor = '';
    }
  }

  // ----- Filters -----
  function populateFilters() {
    const models = [...new Set(state.runs.map(r => r.model))].sort();
    const gpus = [...new Set(state.runs.map(r => r.gpu))].sort();
    const precisions = [...new Set(state.runs.map(r => precisionLabel(r)).filter(Boolean))].sort();
    dom.filterModel.innerHTML = '<option value="">All Models</option>' +
      models.map(m => `<option value="${esc(m)}">${esc(m)}</option>`).join('');

    dom.filterGpu.innerHTML = '<option value="">All GPUs</option>' +
      gpus.map(g => `<option value="${esc(g)}">${esc(g)}</option>`).join('');

    dom.filterPrecision.innerHTML = '<option value="">All Precisions</option>' +
      precisions.map(p => `<option value="${esc(p)}">${esc(p)}</option>`).join('');
  }

  function applyFilters() {
    const modelFilter = dom.filterModel.value;
    const gpuFilter = dom.filterGpu.value;
    const precisionFilter = dom.filterPrecision.value;

    state.filteredRuns = state.runs.filter(r => {
      if (modelFilter && r.model !== modelFilter) return false;
      if (gpuFilter && r.gpu !== gpuFilter) return false;
      if (precisionFilter && precisionLabel(r) !== precisionFilter) return false;
      return true;
    });

    state.filteredRuns.sort((a, b) => b.timestamp.localeCompare(a.timestamp));
    dom.runCountBadge.textContent = `${state.filteredRuns.length} run${state.filteredRuns.length !== 1 ? 's' : ''}`;

    renderSidebar();
    renderTable();
    renderChart();
  }

  function onFilterChange() { applyFilters(); }
  function onMetricChange() { renderChart(); }

  // Search is part of the rendered-set decision (single source of truth), so it
  // survives re-renders triggered by filter changes. Debounced to avoid
  // rebuilding the table on every keystroke.
  const onTableSearch = debounce(() => {
    state.searchQuery = dom.tableSearch.value.toLowerCase().trim();
    renderTable();
  }, 150);

  // runHaystack is the lowercased text a table search matches against.
  function runHaystack(r) {
    return `${r.model} ${r.gpu} ${r.timestamp} ${precisionLabel(r)}`.toLowerCase();
  }

  // ----- Sidebar -----
  function renderSidebar() {
    if (state.filteredRuns.length === 0) {
      dom.sidebarList.innerHTML =
        '<div style="padding:8px;color:var(--text-muted);font-size:0.8rem;">No matching runs</div>';
      return;
    }

    let html = '';
    state.filteredRuns.forEach(r => {
      const key = `${r.model}/${r.gpu}/${r.timestamp}`;
      const isActive = state.selectedKey === key ? ' active' : '';
      const prec = precisionLabel(r);
      const precHtml = prec
        ? `<span class="sidebar-precision-chip">${esc(prec)}</span>`
        : '';
      html += `<div class="sidebar-item${isActive}" data-model="${esc(r.model)}" data-gpu="${esc(r.gpu)}" data-ts="${esc(r.timestamp)}">
        <div class="sidebar-item-top">
          <span class="sidebar-item-title">${esc(r.model)}</span>
          ${precHtml}
        </div>
        <span class="sidebar-item-sub">${esc(r.gpu)} · ${esc(formatTimestamp(r.timestamp))}</span>
      </div>`;
    });

    dom.sidebarList.innerHTML = html;
    dom.sidebarList.querySelectorAll('.sidebar-item').forEach(el => {
      el.addEventListener('click', () => selectRun(el.dataset.model, el.dataset.gpu, el.dataset.ts));
    });
  }

  // ----- Run Selection -----
  let _selectRunController = null;

  async function selectRun(model, gpu, timestamp) {
    if (_selectRunController) _selectRunController.abort();
    _selectRunController = new AbortController();
    const { signal } = _selectRunController;

    const key = `${model}/${gpu}/${timestamp}`;
    state.selectedKey = key;

    dom.sidebarList.querySelectorAll('.sidebar-item').forEach(el => {
      el.classList.toggle('active',
        el.dataset.model === model && el.dataset.gpu === gpu && el.dataset.ts === timestamp);
    });
    dom.tableBody.querySelectorAll('tr').forEach(row => {
      row.classList.toggle('active-row',
        row.dataset.model === model && row.dataset.gpu === gpu && row.dataset.ts === timestamp);
    });

    try {
      state.selectedRun = await fetchRunDetail(model, gpu, timestamp, signal);
      renderKPIs();
      renderConfigBar();
      renderCost();
    } catch (err) {
      if (err.name === 'AbortError') return;
      console.error('Error loading run detail:', err);
      showError('Failed to load run details.');
    }
  }

  // ----- KPI Cards -----
  function renderKPIs() {
    const r = state.selectedRun;
    if (!r) return;
    dom.valTotalThroughput.textContent = fmtNum(r.total_token_throughput);
    dom.valOutputThroughput.textContent = fmtNum(r.output_throughput);
    dom.valTtft.textContent = fmtNum(r.mean_ttft_ms);
    dom.valTpot.textContent = fmtNum(r.mean_tpot_ms);
    renderVerdictKPI();
  }

  // Rent-vs-buy verdict at the market-median tier, shown in the top KPI row.
  function renderVerdictKPI() {
    const r = state.selectedRun;
    const cm = r && r.cost_metrics;
    const ladder = cm && cm.rent_vs_buy_analysis;
    const tiers = ladder && Array.isArray(ladder.tiers) ? ladder.tiers : [];
    const median = tiers.find(t => t.tier === 'dedicated_median');
    if (!median) {
      dom.valVerdict.textContent = '—';
      dom.valVerdict.style.color = '';
      return;
    }
    const buy = median.verdict === 'buy';
    dom.valVerdict.textContent = buy ? 'BUY' : 'RENT';
    dom.valVerdict.style.color = buy ? 'hsl(142, 60%, 55%)' : 'hsl(38, 90%, 60%)';
  }

  // ----- Config Bar -----
  function renderConfigBar() {
    const r = state.selectedRun;
    const mc = r && r.model_config;

    if (!mc) {
      dom.configBar.classList.add('hidden');
      return;
    }

    dom.configBar.classList.remove('hidden');

    const tp = mc.tp || 1;
    const pp = mc.pp || 1;
    dom.cfgTp.textContent = tp > 1 ? `TP${tp}` : '';
    dom.cfgPp.textContent = pp > 1 ? `PP${pp}` : '';

    const quantLabel = mc.quantization ? mc.quantization.toUpperCase() : '';
    const dtypeLabel = mc.dtype && mc.dtype !== 'auto' ? mc.dtype.toUpperCase() : '';

    dom.cfgQuant.textContent = quantLabel || dtypeLabel || 'DEFAULT';
    applyPrecisionStyle(dom.cfgQuant, quantLabel || dtypeLabel || 'DEFAULT');

    dom.cfgDtype.textContent = (quantLabel && dtypeLabel) ? dtypeLabel : '';
    dom.cfgMaxlen.textContent = mc.max_model_len ? `maxlen ${mc.max_model_len.toLocaleString()}` : '';
    dom.cfgGmem.textContent = mc.gpu_memory_utilization ? `mem ${mc.gpu_memory_utilization}` : '';
  }

  // ----- Cost Analysis -----
  function renderCost() {
    const r = state.selectedRun;
    const cm = r && r.cost_metrics;
    const sec = $('#cost-section');
    if (!sec) return;

    const ladder = cm && cm.rent_vs_buy_analysis;
    if (!cm || !ladder || !Array.isArray(ladder.tiers) || ladder.tiers.length === 0) {
      sec.classList.add('hidden');
      return;
    }
    sec.classList.remove('hidden');

    $('#cost-owned').textContent = fmtUSD(ladder.owned_fully_loaded_usd);
    $('#cost-energy').textContent = fmtUSD(ladder.owned_energy_cost_usd);
    $('#cost-blended').textContent = cm.corespan_infra
      ? fmtUSD(cm.corespan_infra.blended_cost_per_1m_tokens_usd, 2)
      : '—';
    $('#cost-watts').textContent = fmtNum(cm.avg_gpu_power_watts);

    $('#cost-ladder-body').innerHTML = ladder.tiers.map(t => {
      const buy = t.verdict === 'buy';
      const cls = buy ? 'verdict-buy' : 'verdict-rent';
      const sign = Number(t.savings_usd_vs_buy) >= 0 ? '+' : '−';
      const mag = fmtUSD(Math.abs(Number(t.savings_usd_vs_buy)));
      return `<tr>
        <td>${esc(t.label || t.tier)}</td>
        <td class="mono">$${fmtNum(t.rental_hourly_usd)}</td>
        <td class="mono">${fmtUSD(t.rental_total_usd)}</td>
        <td class="mono ${cls}">${sign}${mag}</td>
        <td><span class="verdict-chip ${cls}">${buy ? 'BUY' : 'RENT'}</span></td>
      </tr>`;
    }).join('');

    const median = ladder.tiers.find(t => t.tier === 'dedicated_median');
    const vb = $('#cost-verdict');
    if (median) {
      const buy = median.verdict === 'buy';
      vb.textContent = buy ? 'BUY wins at market median' : 'RENT wins at market median';
      vb.className = 'cost-verdict-badge ' + (buy ? 'verdict-buy' : 'verdict-rent');
    } else {
      vb.textContent = '';
      vb.className = 'cost-verdict-badge';
    }

    const fn = $('#cost-footnote');
    if (fn) {
      const capex = fmtUSD(ladder.owned_capex_only_usd);
      fn.textContent = `Owned = amortized CapEx (${capex}) + electricity over a ${fmtNum(ladder.window_hours)}h window · ${ladder.effective_num_gpus} GPU(s) · pricing as of ${cm.pricing_source_date || 'n/a'}.`;
    }
  }

  // ----- Chart -----
  function renderChart() {
    const metricKey = dom.filterMetric.value;
    const metricLabels = {
      total_token_throughput: 'Total Token Throughput (tok/s)',
      output_throughput: 'Output Throughput (tok/s)',
      request_throughput: 'Request Throughput (req/s)',
      mean_ttft_ms: 'Mean TTFT (ms)',
      mean_tpot_ms: 'Mean TPOT (ms)',
    };

    const runs = [...state.filteredRuns].reverse();
    const labels = runs.map(r => {
      const prec = precisionLabel(r);
      const mc = r.summary && r.summary.model_config;
      const tp = mc && mc.tp > 1 ? ` TP${mc.tp}` : '';
      return `${formatTimestamp(r.timestamp)}${prec ? ' · ' + prec : ''}${tp}`;
    });
    const data = runs.map(r => {
      if (r.summary && r.summary[metricKey] != null) return r.summary[metricKey];
      return null;
    });

    if (state.chart) {
      state.chart.data.labels = labels;
      state.chart.data.datasets[0].label = metricLabels[metricKey] || metricKey;
      state.chart.data.datasets[0].data = data;
      state.chart.update();
      return;
    }

    const ctx = dom.chartCanvas.getContext('2d');
    const gradient = ctx.createLinearGradient(0, 0, 0, 320);
    gradient.addColorStop(0, 'hsla(174, 72%, 46%, 0.3)');
    gradient.addColorStop(1, 'hsla(174, 72%, 46%, 0.02)');

    state.chart = new Chart(ctx, {
      type: 'line',
      data: {
        labels,
        datasets: [{
          label: metricLabels[metricKey] || metricKey,
          data,
          borderColor: 'hsl(174, 72%, 46%)',
          backgroundColor: gradient,
          borderWidth: 2.5,
          pointBackgroundColor: 'hsl(174, 72%, 46%)',
          pointBorderColor: 'hsl(220, 18%, 14%)',
          pointBorderWidth: 2,
          pointRadius: 4,
          pointHoverRadius: 6,
          tension: 0.3,
          fill: true,
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: { mode: 'index', intersect: false },
        plugins: {
          legend: {
            display: true,
            labels: {
              color: 'hsl(220, 10%, 55%)',
              font: { family: "'Inter', sans-serif", size: 12 },
              boxWidth: 12, boxHeight: 2,
            }
          },
          tooltip: {
            backgroundColor: 'hsl(220, 18%, 14%)',
            titleColor: 'hsl(0, 0%, 92%)',
            bodyColor: 'hsl(0, 0%, 92%)',
            borderColor: 'hsla(220, 20%, 30%, 0.4)',
            borderWidth: 1,
            cornerRadius: 8,
            padding: 12,
            titleFont: { family: "'Inter', sans-serif", weight: '600' },
            bodyFont: { family: "'JetBrains Mono', monospace", size: 13 },
          }
        },
        scales: {
          x: {
            ticks: {
              color: 'hsl(220, 10%, 40%)',
              font: { family: "'Inter', sans-serif", size: 11 },
              maxRotation: 45,
            },
            grid: { color: 'hsla(220, 20%, 30%, 0.2)' }
          },
          y: {
            beginAtZero: true,
            ticks: {
              color: 'hsl(220, 10%, 40%)',
              font: { family: "'JetBrains Mono', monospace", size: 11 },
            },
            grid: { color: 'hsla(220, 20%, 30%, 0.2)' }
          }
        }
      }
    });
  }

  // ----- Table -----
  function renderTable() {
    if (state.filteredRuns.length === 0) {
      dom.tableBody.innerHTML =
        '<tr><td colspan="10" style="text-align:center;color:var(--text-muted);padding:2rem;">No benchmark runs found</td></tr>';
      return;
    }

    const q = state.searchQuery;
    const rows = q ? state.filteredRuns.filter(r => runHaystack(r).includes(q)) : state.filteredRuns;

    if (rows.length === 0) {
      dom.tableBody.innerHTML =
        `<tr><td colspan="10" style="text-align:center;color:var(--text-muted);padding:2rem;">No runs match “${esc(q)}”</td></tr>`;
      return;
    }

    dom.tableBody.innerHTML = rows.map(r => {
      const s = r.summary || {};
      const mc = s.model_config || {};
      const prec = precisionLabel(r);
      const tppp = formatTPPP(mc);
      const isActive = state.selectedKey === `${r.model}/${r.gpu}/${r.timestamp}` ? ' active-row' : '';
      return `<tr class="${isActive}" data-model="${esc(r.model)}" data-gpu="${esc(r.gpu)}" data-ts="${esc(r.timestamp)}">
        <td>${esc(r.model)}</td>
        <td>${esc(r.gpu)}</td>
        <td>${prec ? `<span class="table-precision-chip" data-prec="${esc(prec)}">${esc(prec)}</span>` : '—'}</td>
        <td class="mono">${tppp}</td>
        <td class="mono">${s.concurrency != null ? s.concurrency : '—'}</td>
        <td class="mono">${fmtNum(s.total_token_throughput)}</td>
        <td class="mono">${fmtNum(s.output_throughput)}</td>
        <td class="mono">${fmtNum(s.mean_ttft_ms)}</td>
        <td class="mono">${fmtNum(s.mean_tpot_ms)}</td>
        <td>${esc(formatTimestamp(r.timestamp))}</td>
      </tr>`;
    }).join('');

    // Colour precision chips in table
    dom.tableBody.querySelectorAll('.table-precision-chip').forEach(el => {
      applyPrecisionStyle(el, el.dataset.prec);
    });

    dom.tableBody.querySelectorAll('tr').forEach(row => {
      if (!row.dataset.model) return;
      row.addEventListener('click', () => selectRun(row.dataset.model, row.dataset.gpu, row.dataset.ts));
    });
  }

  // ----- Helpers -----
  function formatTPPP(mc) {
    if (!mc) return '—';
    const tp = mc.tp || 1;
    const pp = mc.pp || 1;
    if (tp === 1 && pp === 1) return '—';
    if (pp === 1) return `TP${tp}`;
    return `TP${tp} / PP${pp}`;
  }

  function esc(str) {
    if (str == null) return '';
    const div = document.createElement('div');
    div.textContent = String(str);
    return div.innerHTML;
  }

  function debounce(fn, ms) {
    let t;
    return (...args) => {
      clearTimeout(t);
      t = setTimeout(() => fn(...args), ms);
    };
  }

  function fmtNum(val) {
    if (val == null) return '—';
    const num = Number(val);
    return isNaN(num) ? '—' : num.toFixed(2);
  }

  // fmtUSD formats a dollar amount. Pass `decimals` to force a fixed precision;
  // otherwise it adapts — whole dollars for large sums, and enough decimals to
  // reveal sub-cent values (so short-run costs show the real number, not "$0").
  function fmtUSD(val, decimals) {
    if (val == null) return '—';
    const num = Number(val);
    if (isNaN(num)) return '—';
    let d = decimals;
    if (d == null) {
      const abs = Math.abs(num);
      if (abs === 0) d = 2;
      else if (abs >= 1000) d = 0;              // $111,568
      else if (abs >= 1) d = 2;                 // $25.09
      // sub-dollar: ~2 significant figures so small costs show the real value
      else d = Math.min(8, Math.max(2, 1 - Math.floor(Math.log10(abs))));
    }
    return '$' + num.toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d });
  }

  function formatTimestamp(ts) {
    if (!ts) return '—';
    return ts.replace(/T/, ' ').replace(/-(\d{2})-(\d{2})$/, ':$1:$2');
  }

  // Inject .hidden helper and error toast
  const style = document.createElement('style');
  style.textContent = `
    .hidden { display: none !important; }
    .sidebar-item-top {
      display: flex;
      align-items: center;
      gap: 6px;
    }
    .sidebar-precision-chip {
      font-size: 0.65rem;
      font-weight: 700;
      font-family: var(--font-mono);
      padding: 0.1em 0.5em;
      border-radius: 999px;
      background: var(--accent-muted);
      color: var(--accent);
      border: 1px solid var(--border-accent);
      flex-shrink: 0;
    }
    .table-precision-chip {
      display: inline-flex;
      align-items: center;
      padding: 0.15em 0.55em;
      font-size: 0.72rem;
      font-weight: 700;
      font-family: var(--font-mono);
      border-radius: 999px;
      background: var(--accent-muted);
      color: var(--accent);
      border: 1px solid var(--border-accent);
    }
    #cost-section { margin-top: var(--space-lg, 16px); }
    .cost-kpi-row {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
      gap: var(--space-md, 12px);
      margin-bottom: var(--space-lg, 16px);
    }
    .cost-ladder-table td { vertical-align: middle; }
    .verdict-buy { color: hsl(142, 60%, 55%); }
    .verdict-rent { color: hsl(38, 90%, 60%); }
    .verdict-chip {
      display: inline-flex;
      align-items: center;
      padding: 0.15em 0.6em;
      font-size: 0.72rem;
      font-weight: 700;
      font-family: var(--font-mono);
      border-radius: 999px;
      border: 1px solid currentColor;
    }
    .cost-verdict-badge {
      font-size: 0.75rem;
      font-weight: 700;
      font-family: var(--font-mono);
      padding: 0.2em 0.7em;
      border-radius: 999px;
      border: 1px solid currentColor;
    }
    .cost-footnote {
      margin-top: var(--space-md, 12px);
      font-size: 0.75rem;
      color: var(--text-muted);
      font-family: var(--font-sans);
    }
    .error-toast {
      position: fixed;
      top: var(--space-lg);
      right: var(--space-lg);
      background: hsla(0, 72%, 50%, 0.15);
      color: hsl(0, 72%, 65%);
      border: 1px solid hsla(0, 72%, 50%, 0.4);
      border-radius: var(--radius-md);
      padding: var(--space-sm) var(--space-lg);
      font-size: 0.85rem;
      font-family: var(--font-sans);
      z-index: 100;
      animation: fadeIn 0.3s ease-out both;
      cursor: pointer;
    }
  `;
  document.head.appendChild(style);

  function showError(msg) {
    const toast = document.createElement('div');
    toast.className = 'error-toast';
    toast.textContent = msg;
    toast.addEventListener('click', () => toast.remove());
    document.body.appendChild(toast);
    setTimeout(() => toast.remove(), 6000);
  }

  document.addEventListener('DOMContentLoaded', init);
})();