// Overview page logic — per-issue charts driven by /api/issues

let chartsInstances = {};

// Load issues for the given date range and render all charts + cards
async function loadOverview(from, to) {
  try {
    const [issues, comments] = await Promise.all([
      fetchAPI(`/api/issues?from=${from}&to=${to}`),
      fetchAPI(`/api/comments/summary?from=${from}&to=${to}`)
    ]);
    updateSummaryCards(issues);
    renderStatusChart(issues);
    renderCostChart(issues);
    renderDurationChart(issues);
    renderReviewersChart(comments);
    renderSeverityChart(comments);
    renderActivityFeed(issues, comments);
  } catch (error) {
    showError('Failed to load overview data: ' + error.message);
  }
}

// --- Summary Cards ---

function updateSummaryCards(issues) {
  const total = issues.length;
  const merged = issues.filter(i => i.pr_merged).length;
  const closed = issues.filter(i => i.pr_closed).length;
  const mergeRate = total > 0 ? (merged / total * 100) : 0;
  const issuesWithCost = issues.filter(i => i.total_cost > 0);
  const totalCost = issuesWithCost.reduce((sum, i) => sum + i.total_cost, 0);
  const avgCost = issuesWithCost.length > 0 ? totalCost / issuesWithCost.length : 0;

  document.getElementById('total-issues').textContent = formatNumber(total);
  document.getElementById('merged-issues').textContent = formatNumber(merged);
  document.getElementById('closed-issues').textContent = formatNumber(closed);
  document.getElementById('merge-rate').textContent = total > 0 ? mergeRate.toFixed(0) + '%' : 'N/A';
  document.getElementById('total-cost').textContent = formatCost(totalCost);
  document.getElementById('avg-cost').textContent = formatCost(avgCost);
}

// --- Charts ---

function destroyChart(key) {
  if (chartsInstances[key]) {
    chartsInstances[key].destroy();
    chartsInstances[key] = null;
  }
}

// Donut: Issues by PR status
function renderStatusChart(issues) {
  const ctx = document.getElementById('status-chart').getContext('2d');
  destroyChart('status');

  const counts = { merged: 0, open: 0, closed: 0 };
  issues.forEach(i => {
    if (i.pr_merged) counts.merged++;
    else if (i.pr_closed) counts.closed++;
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

  const sorted = [...issues]
    .filter(i => i.total_cost > 0)
    .sort((a, b) => b.total_cost - a.total_cost);

  const labels = sorted.map(i => i.jira_key);
  const data = sorted.map(i => i.total_cost);
  const colors = sorted.map(i => i.pr_merged ? '#27ae60' : i.pr_closed ? '#e74c3c' : '#3498db');

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

  const sorted = [...issues]
    .filter(i => i.merge_duration > 0)
    .sort((a, b) => b.merge_duration - a.merge_duration);

  const labels = sorted.map(i => i.jira_key);
  const data = sorted.map(i => i.merge_duration); // in hours
  const colors = sorted.map(i => i.pr_merged ? '#27ae60' : i.pr_closed ? '#e74c3c' : '#3498db');

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

  // PR merge/close events
  (issues || []).forEach(i => {
    if (i.pr_merged && i.merged_at) {
      activityEvents.push({
        type: 'merged',
        icon: 'M',
        text: `<a href="issue.html?key=${encodeURIComponent(i.jira_key)}">${escapeHTML(i.jira_key)}</a> merged`,
        time: new Date(i.merged_at),
        url: i.pr_url
      });
    } else if (i.pr_closed && i.closed_at) {
      activityEvents.push({
        type: 'closed',
        icon: 'C',
        text: `<a href="issue.html?key=${encodeURIComponent(i.jira_key)}">${escapeHTML(i.jira_key)}</a> closed`,
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

// --- Time Range Helpers ---

function dateStr(d) {
  return d.toISOString().split('T')[0];
}

function getTimeRanges() {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());

  return {
    '7d': {
      from: new Date(today.getTime() - 7 * 86400000),
      to: today
    },
    'this-month': {
      from: new Date(today.getFullYear(), today.getMonth(), 1),
      to: today
    },
    'last-month': {
      from: new Date(today.getFullYear(), today.getMonth() - 1, 1),
      to: new Date(today.getFullYear(), today.getMonth(), 0)
    },
    '3m': {
      from: new Date(today.getTime() - 90 * 86400000),
      to: today
    },
    'ytd': {
      from: new Date(today.getFullYear(), 0, 1),
      to: today
    }
  };
}

function applyRange(rangeKey) {
  const ranges = getTimeRanges();
  const range = ranges[rangeKey];
  if (!range) return;

  const from = dateStr(range.from);
  const to = dateStr(range.to);

  // Update active button
  document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
  document.getElementById('range-' + rangeKey).classList.add('active');

  document.getElementById('date-from').value = from;
  document.getElementById('date-to').value = to;

  loadOverview(from, to);
}

function applyCustomDateRange() {
  const from = document.getElementById('date-from').value;
  const to = document.getElementById('date-to').value;

  if (from && to) {
    document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
    loadOverview(from, to);
  }
}

// Setup event handlers
function setupTimeRangeHandlers() {
  document.getElementById('range-7d').addEventListener('click', () => applyRange('7d'));
  document.getElementById('range-this-month').addEventListener('click', () => applyRange('this-month'));
  document.getElementById('range-last-month').addEventListener('click', () => applyRange('last-month'));
  document.getElementById('range-3m').addEventListener('click', () => applyRange('3m'));
  document.getElementById('range-ytd').addEventListener('click', () => applyRange('ytd'));

  document.getElementById('date-from').addEventListener('change', applyCustomDateRange);
  document.getElementById('date-to').addEventListener('change', applyCustomDateRange);
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

// Initialize on page load — default to last 7 days
document.addEventListener('DOMContentLoaded', () => {
  setupTimeRangeHandlers();
  applyRange('7d');
  loadScraperStatus();
});
