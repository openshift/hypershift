// Overview page logic — per-issue charts driven by /api/issues

let chartsInstances = {};
let _overviewIssues = [];
let _overviewComments = [];
let _overviewTelemetrySummary = null;
let _overviewFrom = '';
let _overviewTo = '';

async function loadOverview(from, to) {
  _overviewFrom = from;
  _overviewTo = to;
  try {
    const [issues, comments, telemetrySummary] = await Promise.all([
      fetchAPI(`/api/issues?from=${from}&to=${to}`),
      fetchAPI(`/api/comments/summary?from=${from}&to=${to}`),
      fetchAPI(`/api/telemetry/summary?from=${from}&to=${to}`)
    ]);
    _overviewIssues = issues;
    _overviewComments = comments;
    _overviewTelemetrySummary = telemetrySummary;
    updateComponentChips(extractComponents(issues, i => i.component || 'hypershift'));
    renderOverview();
  } catch (error) {
    showError('Failed to load overview data: ' + error.message);
  }
}

function renderOverview() {
  const issues = filterByComponent(_overviewIssues, i => i.component || 'hypershift');
  const iMap = buildIssueMap(issues);
  const comments = filterCommentsByIssueMap(_overviewComments, buildIssueMap(_overviewIssues));

  updateImpactHero(issues, _overviewFrom, _overviewTo);
  updateSummaryCards(issues, comments, _overviewTelemetrySummary);
  renderStatusChart(issues);
  renderCostChart(issues);
  renderDurationChart(issues);
  renderReviewersChart(comments);
  renderSeverityChart(comments);
  renderActivityFeed(issues, comments);
}

async function loadTrends(rangeKey) {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  let from;
  switch (rangeKey) {
    case '6m': from = new Date(today.getTime() - 180 * 86400000); break;
    case 'ytd': from = new Date(today.getFullYear(), 0, 1); break;
    default: from = new Date(today.getTime() - 90 * 86400000); break;
  }
  const fromStr = dateStr(from);
  const toStr = dateStr(today);

  document.querySelectorAll('.trend-range button').forEach(btn => btn.classList.remove('active'));
  document.getElementById('trend-' + rangeKey).classList.add('active');

  try {
    const trends = await fetchAPI(`/api/trends?from=${fromStr}&to=${toStr}&granularity=weekly`);
    renderTrendMergeRate(trends);
    renderTrendQuality(trends);
    renderTrendCost(trends);
  } catch (error) {
    showError('Failed to load trend data: ' + error.message);
  }
}

// --- Summary Cards ---

function updateSummaryCards(issues, comments, telemetrySummary) {
  const groups = groupByJiraKey(issues);
  const total = groups.length;
  const mergedGroups = groups.filter(g => g.best.pr_merged);
  const merged = mergedGroups.length;
  const mergeRate = total > 0 ? (merged / total * 100) : 0;

  // Avg Quality
  const groupsWithQuality = groups.filter(g => g.best.quality_score != null && g.best.quality_score > 0);
  const avgQuality = groupsWithQuality.length > 0
    ? groupsWithQuality.reduce((sum, g) => sum + g.best.quality_score, 0) / groupsWithQuality.length
    : 0;

  // First-Pass Rate: merged PRs with zero required_change comments
  const reqChangeByIssue = {};
  (comments || []).forEach(c => {
    if (c.severity === 'required_change' && c.issue_id) {
      reqChangeByIssue[c.issue_id] = (reqChangeByIssue[c.issue_id] || 0) + 1;
    }
  });
  const firstPassCount = mergedGroups.filter(g => !reqChangeByIssue[g.best.id]).length;
  const firstPassRate = merged > 0 ? (firstPassCount / merged * 100) : 0;

  // Avg Time-to-Merge
  const mergedWithDuration = mergedGroups.filter(g => g.best.merge_duration > 0);
  const avgMergeDuration = mergedWithDuration.length > 0
    ? mergedWithDuration.reduce((sum, g) => sum + g.best.merge_duration, 0) / mergedWithDuration.length
    : 0;

  // Cost metrics
  const totalCost = groups.reduce((sum, g) => sum + g.totalCost, 0);
  const mergedCost = mergedGroups.reduce((sum, g) => sum + g.totalCost, 0);
  const avgCostMerged = merged > 0 ? mergedCost / merged : 0;
  const wastedCost = groups
    .filter(g => g.best.pr_closed && !g.best.pr_merged)
    .reduce((sum, g) => sum + g.totalCost, 0);

  // Cache Hit Rate
  const cacheRate = telemetrySummary && telemetrySummary.avg_cache_hit_rate_pct != null
    ? telemetrySummary.avg_cache_hit_rate_pct
    : null;

  // Avg Review Cycles
  const groupsWithCycles = groups.filter(g => g.best.review_cycles != null);
  const avgCycles = groupsWithCycles.length > 0
    ? groupsWithCycles.reduce((sum, g) => sum + g.best.review_cycles, 0) / groupsWithCycles.length
    : 0;

  document.getElementById('merge-rate').textContent = total > 0 ? mergeRate.toFixed(0) + '%' : 'N/A';
  document.getElementById('avg-quality').textContent = groupsWithQuality.length > 0 ? avgQuality.toFixed(0) : 'N/A';
  document.getElementById('first-pass-rate').textContent = merged > 0 ? firstPassRate.toFixed(0) + '%' : 'N/A';
  document.getElementById('avg-time-to-merge').textContent = avgMergeDuration > 0 ? formatDuration(avgMergeDuration) : 'N/A';
  document.getElementById('avg-review-cycles').textContent = groupsWithCycles.length > 0 ? avgCycles.toFixed(1) : 'N/A';
  document.getElementById('total-cost').textContent = formatCost(totalCost);
  document.getElementById('avg-cost-merged').textContent = merged > 0 ? formatCost(avgCostMerged) : 'N/A';
  document.getElementById('wasted-cost').textContent = formatCost(wastedCost);
  document.getElementById('cache-hit-rate').textContent = cacheRate != null ? cacheRate.toFixed(1) + '%' : 'N/A';
}

// --- Charts ---

function destroyChart(key) {
  if (chartsInstances[key]) {
    chartsInstances[key].destroy();
    chartsInstances[key] = null;
  }
}

// --- Trend Charts ---

function formatBucketLabel(isoStr) {
  const parts = isoStr.split('-');
  const months = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
  return months[parseInt(parts[1], 10) - 1] + ' ' + parseInt(parts[2], 10);
}

function trendChartOptions(tickCallback) {
  return {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: { display: false },
      tooltip: { mode: 'index', intersect: false }
    },
    scales: {
      x: {
        grid: { display: false },
        ticks: {
          maxRotation: 45,
          minRotation: 0,
          font: { size: 11 },
          autoSkip: true,
          maxTicksLimit: 12
        }
      },
      y: {
        beginAtZero: true,
        ticks: tickCallback ? { callback: tickCallback } : {}
      }
    },
    elements: {
      point: { radius: 4, hoverRadius: 6 }
    }
  };
}

function renderTrendMergeRate(trends) {
  const ctx = document.getElementById('trend-merge-rate');
  if (!ctx || !trends || !trends.length) return;
  destroyChart('trendMergeRate');

  chartsInstances.trendMergeRate = new Chart(ctx.getContext('2d'), {
    type: 'line',
    data: {
      labels: trends.map(t => formatBucketLabel(t.week_start)),
      datasets: [{
        label: 'Merge Rate',
        data: trends.map(t => (t.merge_rate * 100)),
        borderColor: '#27ae60',
        backgroundColor: 'rgba(39, 174, 96, 0.1)',
        fill: true,
        tension: 0.3
      }]
    },
    options: trendChartOptions(v => v + '%')
  });
}

function renderTrendQuality(trends) {
  const ctx = document.getElementById('trend-quality');
  if (!ctx || !trends || !trends.length) return;
  destroyChart('trendQuality');

  chartsInstances.trendQuality = new Chart(ctx.getContext('2d'), {
    type: 'line',
    data: {
      labels: trends.map(t => formatBucketLabel(t.week_start)),
      datasets: [{
        label: 'Quality Score',
        data: trends.map(t => t.avg_quality_score),
        borderColor: '#3498db',
        backgroundColor: 'rgba(52, 152, 219, 0.1)',
        fill: true,
        tension: 0.3
      }]
    },
    options: trendChartOptions()
  });
}

function renderTrendCost(trends) {
  const ctx = document.getElementById('trend-cost');
  if (!ctx || !trends || !trends.length) return;
  destroyChart('trendCost');

  chartsInstances.trendCost = new Chart(ctx.getContext('2d'), {
    type: 'line',
    data: {
      labels: trends.map(t => formatBucketLabel(t.week_start)),
      datasets: [{
        label: 'Avg Cost',
        data: trends.map(t => t.avg_cost),
        borderColor: '#f39c12',
        backgroundColor: 'rgba(243, 156, 18, 0.1)',
        fill: true,
        tension: 0.3
      }]
    },
    options: trendChartOptions(v => '$' + v.toFixed(2))
  });
}

// Donut: Issues by PR status
function renderStatusChart(issues) {
  const ctx = document.getElementById('status-chart').getContext('2d');
  destroyChart('status');

  const groups = groupByJiraKey(issues);
  const counts = { merged: 0, open: 0, closed: 0 };
  groups.forEach(g => {
    if (g.best.pr_merged) counts.merged++;
    else if (g.best.pr_closed) counts.closed++;
    else counts.open++;
  });

  chartsInstances.status = new Chart(ctx, {
    type: 'doughnut',
    data: {
      labels: ['Merged', 'Open', 'Closed'],
      datasets: [{
        data: [counts.merged, counts.open, counts.closed],
        backgroundColor: ['#27ae60', '#3498db', '#e74c3c'],
        borderWidth: 2,
        borderColor: '#fff'
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { position: 'bottom' }
      }
    }
  });
}

// Horizontal bar: Cost per issue (sorted descending)
function renderCostChart(issues) {
  const ctx = document.getElementById('cost-chart').getContext('2d');
  destroyChart('cost');

  const sorted = groupByJiraKey(issues)
    .filter(g => g.totalCost > 0)
    .sort((a, b) => b.totalCost - a.totalCost);

  const labels = sorted.map(g => g.jiraKey);
  const data = sorted.map(g => g.totalCost);
  const colors = sorted.map(g => g.best.pr_merged ? '#27ae60' : g.best.pr_closed ? '#e74c3c' : '#3498db');

  chartsInstances.cost = new Chart(ctx, {
    type: 'bar',
    data: {
      labels,
      datasets: [{
        label: 'Cost (USD)',
        data,
        backgroundColor: colors,
        borderRadius: 4
      }]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { display: false } },
      scales: {
        x: {
          beginAtZero: true,
          ticks: { callback: v => '$' + v.toFixed(2) }
        }
      }
    }
  });
}

// Horizontal bar: Agent duration per issue (sorted descending)
function renderDurationChart(issues) {
  const ctx = document.getElementById('duration-chart').getContext('2d');
  destroyChart('duration');

  const sorted = groupByJiraKey(issues)
    .filter(g => g.best.merge_duration > 0)
    .sort((a, b) => b.best.merge_duration - a.best.merge_duration);

  const labels = sorted.map(g => g.jiraKey);
  const data = sorted.map(g => g.best.merge_duration);
  const colors = sorted.map(g => g.best.pr_merged ? '#27ae60' : g.best.pr_closed ? '#e74c3c' : '#3498db');

  function formatHours(h) {
    if (h < 1) return Math.round(h * 60) + 'm';
    if (h < 24) return Math.round(h) + 'h';
    const days = Math.floor(h / 24);
    const rem = Math.round(h % 24);
    return rem > 0 ? days + 'd ' + rem + 'h' : days + 'd';
  }

  chartsInstances.duration = new Chart(ctx, {
    type: 'bar',
    data: {
      labels,
      datasets: [{
        label: 'Duration',
        data,
        backgroundColor: colors,
        borderRadius: 4
      }]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: ctx => formatHours(ctx.raw)
          }
        }
      },
      scales: {
        x: {
          beginAtZero: true,
          ticks: { callback: v => formatHours(v) }
        }
      }
    }
  });
}

// Horizontal bar: Top reviewers by comment count
function renderReviewersChart(comments) {
  const ctx = document.getElementById('reviewers-chart');
  if (!ctx) return;
  destroyChart('reviewers');

  const counts = {};
  (comments || []).forEach(c => {
    const author = c.author || 'unknown';
    counts[author] = (counts[author] || 0) + 1;
  });

  const sorted = Object.entries(counts).sort((a, b) => b[1] - a[1]).slice(0, 10);

  chartsInstances.reviewers = new Chart(ctx.getContext('2d'), {
    type: 'bar',
    data: {
      labels: sorted.map(e => e[0]),
      datasets: [{
        label: 'Comments',
        data: sorted.map(e => e[1]),
        backgroundColor: '#3498db',
        borderRadius: 4
      }]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { display: false } },
      scales: { x: { beginAtZero: true, ticks: { stepSize: 1 } } }
    }
  });
}

// Doughnut: Comment severity distribution
function renderSeverityChart(comments) {
  const ctx = document.getElementById('severity-chart');
  if (!ctx) return;
  destroyChart('severity');

  const counts = {};
  (comments || []).forEach(c => {
    const sev = c.severity || 'unclassified';
    counts[sev] = (counts[sev] || 0) + 1;
  });

  const colorMap = {
    nitpick: '#2196F3',
    suggestion: '#FF9800',
    required_change: '#E91E63',
    question: '#9C27B0',
    unclassified: '#9E9E9E'
  };

  const labels = Object.keys(counts);
  const data = labels.map(k => counts[k]);
  const colors = labels.map(k => colorMap[k] || '#9E9E9E');

  chartsInstances.severity = new Chart(ctx.getContext('2d'), {
    type: 'doughnut',
    data: {
      labels: labels.map(l => l.replace('_', ' ')),
      datasets: [{
        data,
        backgroundColor: colors,
        borderWidth: 2,
        borderColor: '#fff'
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { position: 'bottom' } }
    }
  });
}

// Recent activity feed with pagination
let activityEvents = [];
let activityPage = 0;
const ACTIVITY_PER_PAGE = 4;

function commentUrl(prUrl, ghCommentId) {
  if (prUrl && ghCommentId) return prUrl + '#discussion_r' + ghCommentId;
  return prUrl || '';
}

function renderActivityFeed(issues, comments) {
  const container = document.getElementById('activity-list');
  if (!container) return;

  activityEvents = [];
  activityPage = 0;

  // PR merge/close events (one per unique Jira key)
  groupByJiraKey(issues || []).forEach(g => {
    const i = g.best;
    if (i.pr_merged && i.merged_at) {
      activityEvents.push({
        type: 'merged',
        icon: 'M',
        text: `<a href="issue.html?id=${i.id}">${escapeHTML(i.jira_key)}</a> merged`,
        time: new Date(i.merged_at),
        url: i.pr_url
      });
    } else if (i.pr_closed && i.closed_at) {
      activityEvents.push({
        type: 'closed',
        icon: 'C',
        text: `<a href="issue.html?id=${i.id}">${escapeHTML(i.jira_key)}</a> closed`,
        time: new Date(i.closed_at),
        url: i.pr_url
      });
    }
  });

  // Recent comments — link directly to the GitHub comment
  (comments || []).forEach(c => {
    if (c.created_at) {
      const url = commentUrl(c.pr_url, c.github_comment_id);
      const prLabel = c.pr_url ? c.pr_url.replace('https://github.com/', '') : 'PR';
      activityEvents.push({
        type: 'comment',
        icon: 'R',
        text: `<strong>${escapeHTML(c.author || 'unknown')}</strong> commented on <a href="${escapeHTML(url)}" target="_blank">${escapeHTML(prLabel)}</a>`,
        time: new Date(c.created_at),
        url: url
      });
    }
  });

  activityEvents.sort((a, b) => b.time - a.time);
  renderActivityPage();
}

function renderActivityPage() {
  const container = document.getElementById('activity-list');
  const pagination = document.getElementById('activity-pagination');
  if (!container) return;

  const total = activityEvents.length;
  const totalPages = Math.max(1, Math.ceil(total / ACTIVITY_PER_PAGE));
  const start = activityPage * ACTIVITY_PER_PAGE;
  const end = Math.min(start + ACTIVITY_PER_PAGE, total);
  const page = activityEvents.slice(start, end);

  if (page.length === 0) {
    container.innerHTML = '<p style="color:var(--text-secondary)">No recent activity.</p>';
    if (pagination) pagination.innerHTML = '';
    return;
  }

  container.innerHTML = page.map(e => {
    const prLink = e.type !== 'comment' && e.url ? ` <a href="${escapeHTML(e.url)}" target="_blank" title="Open PR">[PR]</a>` : '';
    return `<div class="activity-item">
      <div class="activity-icon ${e.type}">${e.icon}</div>
      <div class="activity-content">
        <div class="activity-text">${e.text}${prLink}</div>
        <div class="activity-time">${formatDate(e.time.toISOString())}</div>
      </div>
    </div>`;
  }).join('');

  if (pagination) {
    pagination.innerHTML = `
      <button id="activity-prev" ${activityPage === 0 ? 'disabled' : ''}>Prev</button>
      <span class="page-info">${start + 1}–${end} of ${total}</span>
      <button id="activity-next" ${activityPage >= totalPages - 1 ? 'disabled' : ''}>Next</button>
    `;
    document.getElementById('activity-prev').addEventListener('click', () => {
      if (activityPage > 0) { activityPage--; renderActivityPage(); }
    });
    document.getElementById('activity-next').addEventListener('click', () => {
      if (activityPage < totalPages - 1) { activityPage++; renderActivityPage(); }
    });
  }
}

// --- Scraper Status Banner ---

const STEP_INFO = {
  prow:     { label: 'Prow Scrape',    tip: 'Last time the Prow scraper ran to import new jira-agent job runs, issues, and phase metrics from CI build logs' },
  github:   { label: 'GitHub Sync',    tip: 'Last time PR state, diff stats, and review comments were refreshed from GitHub' },
  classify: { label: 'Classification', tip: 'Last time the comment classification job ran to assign severity and topic labels to review comments using Claude' }
};

function timeAgo(isoStr) {
  const ms = Date.now() - new Date(isoStr).getTime();
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return mins + 'm ago';
  const hours = Math.floor(mins / 60);
  if (hours < 24) return hours + 'h ago';
  const days = Math.floor(hours / 24);
  return days + 'd ago';
}

function staleness(isoStr, step) {
  const hours = (Date.now() - new Date(isoStr).getTime()) / 3600000;
  const threshold = step === 'classify' ? 48 : 24;
  if (hours > threshold * 2) return 'error';
  if (hours > threshold) return 'stale';
  return 'ok';
}

async function loadScraperStatus() {
  const container = document.getElementById('scraper-status');
  if (!container) return;
  try {
    const runs = await fetchAPI('/api/scraper-status');
    const byStep = {};
    (runs || []).forEach(r => { byStep[r.step] = r; });

    container.innerHTML = Object.keys(STEP_INFO).map(step => {
      const info = STEP_INFO[step];
      const r = byStep[step];
      if (!r) {
        return `<div class="scraper-status-item">
          <span class="step-dot stale"></span>
          <span class="step-label">${escapeHTML(info.label)} <span class="info-tip" data-tip="${escapeHTML(info.tip)}">i</span></span>
          <span class="step-time">awaiting first run</span>
        </div>`;
      }
      const dot = r.status === 'failure' ? 'error' : staleness(r.finished_at, step);
      const statusSuffix = r.status === 'failure' ? ' (failed)' : '';
      const ts = new Date(r.finished_at);
      const fmtTime = ts.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) + ' ' + ts.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
      return `<div class="scraper-status-item">
        <span class="step-dot ${dot}"></span>
        <span class="step-label">${escapeHTML(info.label)} <span class="info-tip" data-tip="${escapeHTML(info.tip)}">i</span></span>
        <span class="step-time">${fmtTime} (${timeAgo(r.finished_at)})${statusSuffix}</span>
      </div>`;
    }).join('');
  } catch {
    container.style.display = 'none';
  }
}

// --- Impact Hero ---

function updateImpactHero(issues, from, to) {
  const groups = groupByJiraKey(issues);
  const solved = groups.filter(g => g.best.pr_merged).length;
  const mergedPRs = issues.filter(i => i.pr_merged).length;
  const open = groups.filter(g => !g.best.pr_merged && !g.best.pr_closed).length;

  document.getElementById('hero-solved').textContent = solved;
  document.getElementById('hero-merged').textContent = mergedPRs;
  document.getElementById('hero-open').textContent = open;

  const rangeLabel = document.getElementById('hero-solved-range');
  if (rangeLabel) {
    const [fy, fm, fd] = from.split('-').map(Number);
    const [ty, tm, td] = to.split('-').map(Number);
    const months = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
    rangeLabel.textContent = months[fm - 1] + ' ' + fd + ' – ' + months[tm - 1] + ' ' + td + ', ' + ty;
  }

  loadImpactTrend(from, to);
}

async function loadImpactTrend(from, to) {
  try {
    const data = await fetchAPI(`/api/outcomes/trends?from=${from}&to=${to}&granularity=weekly`);
    renderImpactTrend(data);
  } catch {
    // silently degrade — hero numbers still show
  }
}

function renderImpactTrend(data) {
  const ctx = document.getElementById('impact-trend');
  if (!ctx || !data || !data.length) return;
  destroyChart('impactTrend');

  chartsInstances.impactTrend = new Chart(ctx.getContext('2d'), {
    type: 'line',
    data: {
      labels: data.map(d => formatBucketLabel(d.week_start)),
      datasets: [{
        label: 'Cumulative Merged',
        data: data.map(d => d.cum_merged),
        borderColor: '#27ae60',
        backgroundColor: 'rgba(39, 174, 96, 0.15)',
        fill: true,
        tension: 0.3,
        pointRadius: 2,
        pointHoverRadius: 4,
        borderWidth: 2
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: ctx => ctx.parsed.y + ' merged'
          }
        }
      },
      scales: {
        x: {
          display: true,
          grid: { display: false },
          ticks: { font: { size: 10 }, color: 'rgba(255,255,255,0.5)', maxTicksLimit: 6 }
        },
        y: {
          display: true,
          beginAtZero: true,
          grid: { color: 'rgba(255,255,255,0.1)' },
          ticks: { font: { size: 10 }, color: 'rgba(255,255,255,0.5)', stepSize: 1 }
        }
      }
    }
  });
}

document.addEventListener('DOMContentLoaded', () => {
  initComponentFilter(renderOverview);
  initTimeRange(loadOverview);

  document.getElementById('trend-3m').addEventListener('click', () => loadTrends('3m'));
  document.getElementById('trend-6m').addEventListener('click', () => loadTrends('6m'));
  document.getElementById('trend-ytd').addEventListener('click', () => loadTrends('ytd'));
  loadTrends('3m');

  loadScraperStatus();
});
