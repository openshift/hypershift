// Telemetry page logic

let costPhaseChart = null;
let cacheChart = null;
let tokenPhaseChart = null;
let durationPhaseChart = null;
let successChart = null;
let toolUsageChart = null;
let apiLatencyChart = null;
let otelToolChart = null;
let phaseSuccessChart = null;
let allSessions = [];
let _telKeyComponentMap = {};
let _telSummary = null;
let _telToolStats = [];
let _telApiLatency = [];
let currentSort = { key: 'started_at', asc: false };

async function loadTelemetry(from, to) {
  try {
    const [sessions, summary, issues] = await Promise.all([
      fetchAPI(`/api/telemetry?from=${from}&to=${to}`),
      fetchAPI(`/api/telemetry/summary?from=${from}&to=${to}`),
      fetchAPI(`/api/issues?from=${from}&to=${to}`)
    ]);

    const [toolStats, apiLatency] = await Promise.all([
      fetchAPI(`/api/telemetry/tools?from=${from}&to=${to}`).catch(() => []),
      fetchAPI(`/api/telemetry/api-latency?from=${from}&to=${to}`).catch(() => [])
    ]);

    allSessions = sessions || [];
    _telSummary = summary;
    _telToolStats = toolStats || [];
    _telApiLatency = apiLatency || [];
    _telKeyComponentMap = buildKeyComponentMap(issues);
    updateComponentChips(extractComponents(issues, i => i.component || 'hypershift'));
    renderTelemetry();
  } catch (error) {
    showError('Failed to load telemetry data: ' + error.message);
  }
}

function renderTelemetry() {
  const sessions = filterByComponent(allSessions, s => _telKeyComponentMap[s.issue_key] || 'hypershift');
  renderSummaryCardsFromSessions(sessions);
  renderPhaseSuccessChart(sessions);
  renderPhaseEfficiencyTable(sessions);
  renderCostPhaseChart(sessions);
  renderTokenPhaseChart(sessions);
  renderCacheChart(sessions);
  renderDurationPhaseChart(sessions);
  renderSuccessChart(sessions);
  renderToolUsageChart(sessions);
  renderAPILatencyChart(_telApiLatency);
  renderOtelToolChart(_telToolStats);
  applyFilters();
}

function renderSummaryCards(s) {
  document.getElementById('stat-sessions').textContent = formatNumber(s.total_sessions);
  document.getElementById('stat-cost').textContent = formatCost(s.avg_cost_usd);
  document.getElementById('stat-cache').textContent = s.avg_cache_hit_rate_pct.toFixed(1) + '%';
  document.getElementById('stat-ttft').textContent = formatMs(s.avg_ttft_ms);
  document.getElementById('stat-tools').textContent = s.avg_tool_calls.toFixed(1);
  document.getElementById('stat-subagents').textContent = s.avg_subagents.toFixed(1);
  document.getElementById('stat-duration').textContent = formatMs(s.avg_duration_ms);
  document.getElementById('stat-turns').textContent = s.avg_num_turns.toFixed(1);
}

function renderSummaryCardsFromSessions(sessions) {
  const n = sessions.length;
  if (n === 0) {
    renderSummaryCards({
      total_sessions: 0, avg_cost_usd: 0, avg_cache_hit_rate_pct: 0,
      avg_ttft_ms: 0, avg_tool_calls: 0, avg_subagents: 0,
      avg_duration_ms: 0, avg_num_turns: 0
    });
    return;
  }
  const avg = (arr, fn) => arr.reduce((s, x) => s + fn(x), 0) / arr.length;
  renderSummaryCards({
    total_sessions: n,
    avg_cost_usd: avg(sessions, s => s.total_cost_usd || 0),
    avg_cache_hit_rate_pct: avg(sessions, s => s.cache_hit_rate_pct || 0),
    avg_ttft_ms: avg(sessions, s => s.ttft_ms || 0),
    avg_tool_calls: avg(sessions, s => s.total_tool_calls || 0),
    avg_subagents: avg(sessions, s => s.num_subagents || 0),
    avg_duration_ms: avg(sessions, s => s.duration_ms || 0),
    avg_num_turns: avg(sessions, s => s.num_turns || 0),
  });
}

function phaseLabel(p) {
  if (p === 'pr-creation') return 'PR Creation';
  return p.charAt(0).toUpperCase() + p.slice(1);
}

function formatMs(ms) {
  if (ms == null || isNaN(ms) || ms <= 0) return 'N/A';
  if (ms < 1000) return Math.round(ms) + 'ms';
  const sec = ms / 1000;
  if (sec < 60) return sec.toFixed(1) + 's';
  const min = Math.floor(sec / 60);
  const remSec = Math.round(sec % 60);
  return min + 'm ' + remSec + 's';
}

function aggregateByPhase(sessions) {
  const phaseOrder = ['solve', 'review', 'fix', 'pr-creation'];
  const phases = {};
  phaseOrder.forEach(p => { phases[p] = { total: 0, success: 0, error: 0, costSum: 0, durationSum: 0 }; });

  sessions.forEach(s => {
    const phase = s.phase in phases ? s.phase : 'solve';
    phases[phase].total++;
    if (s.is_error === 1 || s.result === 'error') {
      phases[phase].error++;
    } else {
      phases[phase].success++;
    }
    phases[phase].costSum += s.total_cost_usd || 0;
    phases[phase].durationSum += s.duration_ms || 0;
  });

  return phaseOrder.map(p => ({
    phase: p,
    label: phaseLabel(p),
    ...phases[p],
    successRate: phases[p].total > 0 ? (phases[p].success / phases[p].total * 100) : 0,
    avgCost: phases[p].total > 0 ? (phases[p].costSum / phases[p].total) : 0,
    avgDuration: phases[p].total > 0 ? (phases[p].durationSum / phases[p].total) : 0,
  }));
}

function renderPhaseSuccessChart(sessions) {
  const ctx = document.getElementById('phase-success-chart').getContext('2d');
  if (phaseSuccessChart) phaseSuccessChart.destroy();

  const data = aggregateByPhase(sessions);

  phaseSuccessChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: data.map(d => d.label),
      datasets: [
        {
          label: 'Success',
          data: data.map(d => d.success),
          backgroundColor: '#27ae60',
          borderRadius: 4
        },
        {
          label: 'Error',
          data: data.map(d => d.error),
          backgroundColor: '#e74c3c',
          borderRadius: 4
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { position: 'bottom' } },
      scales: {
        x: { stacked: true },
        y: { stacked: true, beginAtZero: true, ticks: { stepSize: 1 } }
      }
    }
  });
}

function renderPhaseEfficiencyTable(sessions) {
  const tbody = document.getElementById('phase-efficiency-tbody');
  const data = aggregateByPhase(sessions);

  if (data.every(d => d.total === 0)) {
    tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;">No session data available.</td></tr>';
    return;
  }

  tbody.innerHTML = data.map(d => {
    const rateColor = d.successRate >= 80 ? '#27ae60' : d.successRate >= 50 ? '#f39c12' : '#e74c3c';
    return `<tr>
      <td><strong>${d.label}</strong></td>
      <td>${formatNumber(d.total)}</td>
      <td style="color: ${rateColor}">${d.successRate.toFixed(0)}%</td>
      <td>${formatCost(d.avgCost)}</td>
      <td>${formatMs(d.avgDuration)}</td>
      <td>${formatCost(d.costSum)}</td>
    </tr>`;
  }).join('');
}

function renderCostPhaseChart(sessions) {
  const ctx = document.getElementById('cost-phase-chart').getContext('2d');
  if (costPhaseChart) costPhaseChart.destroy();

  const phaseCosts = { solve: 0, review: 0, fix: 0, 'pr-creation': 0 };
  sessions.forEach(s => {
    const phase = s.phase in phaseCosts ? s.phase : 'solve';
    phaseCosts[phase] += s.total_cost_usd || 0;
  });

  costPhaseChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: Object.keys(phaseCosts).map(phaseLabel),
      datasets: [{
        label: 'Total Cost (USD)',
        data: Object.values(phaseCosts),
        backgroundColor: ['#4a9eff', '#ff6b6b', '#ffd93d', '#6bcb77']
      }]
    },
    options: {
      responsive: true,
      plugins: { legend: { display: false } },
      scales: { y: { beginAtZero: true, ticks: { callback: v => '$' + v.toFixed(2) } } }
    }
  });
}

function renderCacheChart(sessions) {
  const ctx = document.getElementById('cache-chart').getContext('2d');
  if (cacheChart) cacheChart.destroy();

  // Group by date
  const byDate = {};
  sessions.forEach(s => {
    const date = (s.started_at || '').split('T')[0];
    if (!date) return;
    if (!byDate[date]) byDate[date] = [];
    byDate[date].push(s.cache_hit_rate_pct || 0);
  });

  const dates = Object.keys(byDate).sort();
  const avgs = dates.map(d => {
    const vals = byDate[d];
    return vals.reduce((a, b) => a + b, 0) / vals.length;
  });

  cacheChart = new Chart(ctx, {
    type: 'line',
    data: {
      labels: dates,
      datasets: [{
        label: 'Avg Cache Hit Rate %',
        data: avgs,
        borderColor: '#4a9eff',
        backgroundColor: 'rgba(74, 158, 255, 0.1)',
        fill: true,
        tension: 0.3
      }]
    },
    options: {
      responsive: true,
      scales: {
        y: { beginAtZero: true, max: 100, ticks: { callback: v => v + '%' } }
      }
    }
  });
}

function renderTokenPhaseChart(sessions) {
  const ctx = document.getElementById('token-phase-chart').getContext('2d');
  if (tokenPhaseChart) tokenPhaseChart.destroy();

  const phases = ['solve', 'review', 'fix', 'pr-creation'];
  const input = [0, 0, 0, 0];
  const output = [0, 0, 0, 0];
  const cacheRead = [0, 0, 0, 0];

  sessions.forEach(s => {
    const idx = phases.indexOf(s.phase);
    if (idx < 0) return;
    input[idx] += s.input_tokens || 0;
    output[idx] += s.output_tokens || 0;
    cacheRead[idx] += s.cache_read_input_tokens || 0;
  });

  tokenPhaseChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: phases.map(phaseLabel),
      datasets: [
        { label: 'Input Tokens', data: input, backgroundColor: '#4a9eff' },
        { label: 'Output Tokens', data: output, backgroundColor: '#ff6b6b' },
        { label: 'Cache Read', data: cacheRead, backgroundColor: '#6bcb77' }
      ]
    },
    options: {
      responsive: true,
      plugins: { legend: { position: 'top' } },
      scales: {
        x: { stacked: true },
        y: { stacked: true, beginAtZero: true, ticks: { callback: v => (v >= 1e6 ? (v / 1e6).toFixed(1) + 'M' : v >= 1e3 ? (v / 1e3).toFixed(0) + 'K' : v) } }
      }
    }
  });
}

function renderDurationPhaseChart(sessions) {
  const ctx = document.getElementById('duration-phase-chart').getContext('2d');
  if (durationPhaseChart) durationPhaseChart.destroy();

  const phases = ['solve', 'review', 'fix', 'pr-creation'];
  const sums = [0, 0, 0, 0];
  const counts = [0, 0, 0, 0];

  sessions.forEach(s => {
    const idx = phases.indexOf(s.phase);
    if (idx < 0 || !s.duration_ms) return;
    sums[idx] += s.duration_ms;
    counts[idx]++;
  });

  const avgs = sums.map((s, i) => counts[i] > 0 ? s / counts[i] : 0);

  durationPhaseChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: phases.map(phaseLabel),
      datasets: [{
        label: 'Avg Duration',
        data: avgs,
        backgroundColor: ['#4a9eff', '#ff6b6b', '#ffd93d', '#6bcb77']
      }]
    },
    options: {
      responsive: true,
      plugins: {
        legend: { display: false },
        tooltip: { callbacks: { label: ctx => formatMs(ctx.raw) } }
      },
      scales: { y: { beginAtZero: true, ticks: { callback: v => formatMs(v) } } }
    }
  });
}

function renderSuccessChart(sessions) {
  const ctx = document.getElementById('success-chart').getContext('2d');
  if (successChart) successChart.destroy();

  let success = 0, failure = 0;
  sessions.forEach(s => {
    if (s.result === 'success') success++;
    else failure++;
  });

  const total = success + failure;
  const pct = total > 0 ? ((success / total) * 100).toFixed(1) : '0.0';

  successChart = new Chart(ctx, {
    type: 'doughnut',
    data: {
      labels: ['Success', 'Failure'],
      datasets: [{
        data: [success, failure],
        backgroundColor: ['#6bcb77', '#ff6b6b'],
        borderWidth: 0
      }]
    },
    options: {
      responsive: true,
      cutout: '60%',
      plugins: {
        legend: { position: 'bottom' },
        tooltip: { callbacks: { label: ctx => ctx.label + ': ' + ctx.raw + ' (' + (total > 0 ? ((ctx.raw / total) * 100).toFixed(1) : '0') + '%)' } }
      }
    },
    plugins: [{
      id: 'centerText',
      afterDraw(chart) {
        const { ctx: c, chartArea: { top, bottom, left, right } } = chart;
        const cx = (left + right) / 2;
        const cy = (top + bottom) / 2;
        c.save();
        c.font = 'bold 24px sans-serif';
        c.fillStyle = getComputedStyle(document.body).getPropertyValue('--text-primary') || '#e0e0e0';
        c.textAlign = 'center';
        c.textBaseline = 'middle';
        c.fillText(pct + '%', cx, cy);
        c.restore();
      }
    }]
  });
}

function renderToolUsageChart(sessions) {
  const ctx = document.getElementById('tool-usage-chart').getContext('2d');
  if (toolUsageChart) toolUsageChart.destroy();

  const toolCounts = {};
  sessions.forEach(s => {
    if (!s.tool_call_breakdown) return;
    try {
      const breakdown = JSON.parse(s.tool_call_breakdown);
      for (const [tool, count] of Object.entries(breakdown)) {
        toolCounts[tool] = (toolCounts[tool] || 0) + (parseInt(count, 10) || 0);
      }
    } catch (e) { /* skip malformed */ }
  });

  const sorted = Object.entries(toolCounts).sort((a, b) => b[1] - a[1]).slice(0, 10);
  const labels = sorted.map(e => e[0]);
  const data = sorted.map(e => e[1]);

  toolUsageChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [{
        label: 'Total Calls',
        data: data,
        backgroundColor: '#4a9eff'
      }]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      plugins: { legend: { display: false } },
      scales: { x: { beginAtZero: true } }
    }
  });
}

function renderAPILatencyChart(points) {
  const ctx = document.getElementById('api-latency-chart').getContext('2d');
  if (apiLatencyChart) apiLatencyChart.destroy();

  const buckets = { '0-1s': 0, '1-5s': 0, '5-10s': 0, '10-30s': 0, '30s+': 0 };
  (points || []).forEach(p => {
    const s = (p.duration_ms || 0) / 1000;
    if (s < 1) buckets['0-1s']++;
    else if (s < 5) buckets['1-5s']++;
    else if (s < 10) buckets['5-10s']++;
    else if (s < 30) buckets['10-30s']++;
    else buckets['30s+']++;
  });

  apiLatencyChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: Object.keys(buckets),
      datasets: [{
        label: 'API Requests',
        data: Object.values(buckets),
        backgroundColor: ['#6bcb77', '#4a9eff', '#ffd93d', '#ff9f43', '#ff6b6b']
      }]
    },
    options: {
      responsive: true,
      plugins: { legend: { display: false } },
      scales: { y: { beginAtZero: true, title: { display: true, text: 'Request Count' } } }
    }
  });
}

function renderOtelToolChart(stats) {
  const ctx = document.getElementById('otel-tool-chart').getContext('2d');
  if (otelToolChart) otelToolChart.destroy();

  const sorted = (stats || []).sort((a, b) => b.total_calls - a.total_calls).slice(0, 15);
  if (sorted.length === 0) {
    otelToolChart = new Chart(ctx, {
      type: 'bar',
      data: { labels: ['No data'], datasets: [{ data: [0], backgroundColor: '#666' }] },
      options: { indexAxis: 'y', responsive: true, plugins: { legend: { display: false } } }
    });
    return;
  }

  const labels = sorted.map(s => s.tool_name);
  const rates = sorted.map(s => (s.success_rate * 100));
  const colors = rates.map(r => r >= 95 ? '#6bcb77' : r >= 80 ? '#ffd93d' : '#ff6b6b');

  otelToolChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [{
        label: 'Success Rate %',
        data: rates,
        backgroundColor: colors
      }]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: ctx => {
              const stat = sorted[ctx.dataIndex];
              return `${ctx.raw.toFixed(1)}% success (${stat.total_calls} calls, avg ${formatMs(stat.avg_duration_ms)})`;
            }
          }
        }
      },
      scales: { x: { beginAtZero: true, max: 100, ticks: { callback: v => v + '%' } } }
    }
  });
}

function applyFilters() {
  const search = (document.getElementById('filter-search').value || '').toLowerCase().trim();
  const phase = document.getElementById('filter-phase').value;

  const componentFiltered = filterByComponent(allSessions, s => _telKeyComponentMap[s.issue_key] || 'hypershift');
  const filtered = componentFiltered.filter(s => {
    if (search && !(s.issue_key || '').toLowerCase().includes(search)) return false;
    if (phase && s.phase !== phase) return false;
    return true;
  });

  document.getElementById('session-count').textContent = `${filtered.length} of ${componentFiltered.length} sessions`;

  const sorted = sortSessions(filtered);
  renderSessionsTable(sorted);
}

function sortSessions(sessions) {
  const key = currentSort.key;
  const dir = currentSort.asc ? 1 : -1;

  return [...sessions].sort((a, b) => {
    let va = a[key], vb = b[key];
    if (typeof va === 'string') va = va || '';
    if (typeof vb === 'string') vb = vb || '';
    if (typeof va === 'number' && typeof vb === 'number') return (va - vb) * dir;
    return String(va).localeCompare(String(vb)) * dir;
  });
}

function renderSessionsTable(sessions) {
  const tbody = document.getElementById('sessions-tbody');
  if (sessions.length === 0) {
    tbody.innerHTML = '<tr><td colspan="12" style="text-align:center;">No telemetry data found for this period.</td></tr>';
    return;
  }

  tbody.innerHTML = sessions.map(s => {
    const date = s.started_at ? new Date(s.started_at).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) : '--';
    const model = (s.model || '').replace('claude-', '').replace(/-\d{8}$/, '');
    const resultClass = s.result === 'success' ? 'badge-merged' : s.result === 'failure' ? 'badge-closed' : 'badge-open';

    return `<tr>
      <td>${escapeHTML(date)}</td>
      <td><a href="https://issues.redhat.com/browse/${escapeHTML(s.issue_key)}" target="_blank">${escapeHTML(s.issue_key)}</a></td>
      <td>${escapeHTML(s.phase)}</td>
      <td>${escapeHTML(model)}</td>
      <td>${formatCost(s.total_cost_usd)}</td>
      <td>${formatMs(s.duration_ms)}</td>
      <td>${s.num_turns}</td>
      <td>${(s.cache_hit_rate_pct || 0).toFixed(1)}%</td>
      <td>${s.total_tool_calls}</td>
      <td>${s.num_subagents}</td>
      <td>${formatMs(s.ttft_ms)}</td>
      <td><span class="badge ${resultClass}">${escapeHTML(s.result || 'unknown')}</span></td>
    </tr>`;
  }).join('');
}

document.addEventListener('DOMContentLoaded', () => {
  initComponentFilter(renderTelemetry);
  initTimeRange(loadTelemetry);

  document.getElementById('filter-search').addEventListener('input', applyFilters);
  document.getElementById('filter-phase').addEventListener('change', applyFilters);

  const toggle = document.getElementById('sessions-toggle');
  const panel = document.getElementById('sessions-panel');
  const icon = toggle.querySelector('.collapse-icon');
  toggle.addEventListener('click', () => {
    panel.classList.toggle('collapsed');
    icon.textContent = panel.classList.contains('collapsed') ? '▶' : '▼';
  });

  document.querySelectorAll('th.sortable').forEach(th => {
    th.addEventListener('click', () => {
      const key = th.dataset.sort;
      if (currentSort.key === key) {
        currentSort.asc = !currentSort.asc;
      } else {
        currentSort = { key, asc: true };
      }
      applyFilters();
    });
  });
});
