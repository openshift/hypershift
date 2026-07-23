// Shared utilities for the JIRA Agent Dashboard

// API wrapper with error handling
async function fetchAPI(path) {
  try {
    const response = await fetch(path);
    if (!response.ok) {
      throw new Error(`API error: ${response.status} ${response.statusText}`);
    }
    return await response.json();
  } catch (error) {
    console.error('API fetch failed:', error);
    throw error;
  }
}

// Format ISO date string to readable format
function formatDate(isoString) {
  if (!isoString) return 'N/A';
  const date = new Date(isoString);
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  });
}

// Format number with comma separators
function formatNumber(n) {
  if (n == null || isNaN(n)) return 'N/A';
  return n.toLocaleString('en-US');
}

// Format cost as USD currency
function formatCost(usd) {
  if (usd == null || isNaN(usd)) return 'N/A';
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  }).format(usd);
}

// Format duration in hours to human-readable string
function formatDuration(hours) {
  if (hours == null || isNaN(hours) || hours <= 0) return 'N/A';

  const totalMinutes = Math.round(hours * 60);
  const days = Math.floor(hours / 24);
  const remainingHours = Math.floor(hours % 24);
  const minutes = totalMinutes % 60;

  if (days > 0) {
    return `${days}d ${remainingHours}h`;
  } else if (hours >= 1) {
    return `${Math.floor(hours)}h ${minutes}m`;
  } else {
    return `${totalMinutes}m`;
  }
}

// Get date range from URL parameters or default to last 90 days
function getDateRange() {
  const params = new URLSearchParams(window.location.search);
  const from = params.get('from');
  const to = params.get('to');

  if (from && to) {
    return { from, to };
  }

  // Default: last 90 days
  const toDate = new Date();
  const fromDate = new Date(toDate.getTime() - 90 * 86400000);

  return {
    from: fromDate.toISOString().split('T')[0],
    to: toDate.toISOString().split('T')[0]
  };
}

// Set date range in URL parameters
function setDateRange(from, to) {
  const url = new URL(window.location);
  url.searchParams.set('from', from);
  url.searchParams.set('to', to);
  window.history.pushState({}, '', url);
}

// Highlight current page in navigation
function highlightCurrentPage() {
  const currentPath = window.location.pathname;
  const navLinks = document.querySelectorAll('nav a');

  navLinks.forEach(link => {
    link.classList.remove('active');
    const linkPath = new URL(link.href).pathname;
    if (linkPath === currentPath) {
      link.classList.add('active');
    }
  });
}

// Display error message
function showError(message, containerId = 'main') {
  const container = document.getElementById(containerId);
  if (container) {
    const errorDiv = document.createElement('div');
    errorDiv.className = 'error';
    errorDiv.textContent = message;
    container.insertBefore(errorDiv, container.firstChild);
  }
}

// Escape HTML to prevent XSS when interpolating into innerHTML
function escapeHTML(str) {
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

// Display loading state
function showLoading(message = 'Loading') {
  return `<div class="loading">${message}</div>`;
}

// Group raw issue rows (sessions) by Jira key, returning one entry per issue
// with aggregated cost and the "best" session (merged > has PR > latest).
function groupByJiraKey(issues) {
  const groups = new Map();
  for (const issue of issues) {
    const key = issue.jira_key;
    if (!groups.has(key)) {
      groups.set(key, []);
    }
    groups.get(key).push(issue);
  }

  const result = [];
  for (const [jiraKey, sessions] of groups) {
    sessions.sort((a, b) => (a.created_at || '').localeCompare(b.created_at || ''));

    const bestSession = sessions.find(s => s.pr_merged)
      || sessions.find(s => s.pr_number > 0)
      || sessions[sessions.length - 1];

    const totalCost = sessions.reduce((sum, s) => sum + (s.total_cost || 0), 0);

    result.push({
      jiraKey,
      sessions,
      count: sessions.length,
      totalCost,
      best: bestSession,
    });
  }
  return result;
}

// Nav toggle
function setupNavToggle() {
  const nav = document.querySelector('nav');
  const h1 = nav.querySelector('h1');

  // Wrap h1 text in span for fade
  const titleText = h1.textContent;
  h1.innerHTML = '<span>' + titleText + '</span>';

  // Create header wrapper
  const header = document.createElement('div');
  header.className = 'nav-header';
  h1.parentNode.insertBefore(header, h1);
  header.appendChild(h1);

  // Create hamburger toggle
  const btn = document.createElement('button');
  btn.className = 'nav-toggle';
  btn.title = 'Toggle sidebar';
  btn.innerHTML = '<span class="bar"></span>';
  header.appendChild(btn);

  const collapsed = localStorage.getItem('nav-collapsed') === 'true';
  if (collapsed) {
    nav.classList.add('collapsed');
  }

  btn.addEventListener('click', () => {
    nav.classList.toggle('collapsed');
    localStorage.setItem('nav-collapsed', nav.classList.contains('collapsed'));
  });
}

// --- Time Range Helpers ---

function dateStr(d) {
  return d.toISOString().split('T')[0];
}

function getTimeRanges() {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());

  return {
    '7d': { from: new Date(today.getTime() - 7 * 86400000), to: today },
    'this-month': { from: new Date(today.getFullYear(), today.getMonth(), 1), to: today },
    'last-month': {
      from: new Date(today.getFullYear(), today.getMonth() - 1, 1),
      to: new Date(today.getFullYear(), today.getMonth(), 0)
    },
    '3m': { from: new Date(today.getTime() - 90 * 86400000), to: today },
    'ytd': { from: new Date(today.getFullYear(), 0, 1), to: today }
  };
}

function initTimeRange(loadFn) {
  function applyRange(rangeKey) {
    const ranges = getTimeRanges();
    const range = ranges[rangeKey];
    if (!range) return;

    const from = dateStr(range.from);
    const to = dateStr(range.to);

    document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
    document.getElementById('range-' + rangeKey).classList.add('active');

    document.getElementById('date-from').value = from;
    document.getElementById('date-to').value = to;

    loadFn(from, to);
  }

  function applyCustomDateRange() {
    const from = document.getElementById('date-from').value;
    const to = document.getElementById('date-to').value;
    if (from && to) {
      document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
      loadFn(from, to);
    }
  }

  document.getElementById('range-7d').addEventListener('click', () => applyRange('7d'));
  document.getElementById('range-this-month').addEventListener('click', () => applyRange('this-month'));
  document.getElementById('range-last-month').addEventListener('click', () => applyRange('last-month'));
  document.getElementById('range-3m').addEventListener('click', () => applyRange('3m'));
  document.getElementById('range-ytd').addEventListener('click', () => applyRange('ytd'));

  document.getElementById('date-from').addEventListener('change', applyCustomDateRange);
  document.getElementById('date-to').addEventListener('change', applyCustomDateRange);

  applyRange('7d');
}

// Build a map of issue.id → {merged, closed, jiraKey} for outcome cross-referencing
function buildIssueMap(issues) {
  const map = {};
  for (const issue of issues) {
    map[issue.id] = {
      merged: issue.pr_merged,
      closed: issue.pr_closed,
      jiraKey: issue.jira_key,
    };
  }
  return map;
}

// Safely destroy and nullify a Chart.js instance
function resetChart(chart) {
  if (chart) chart.destroy();
  return null;
}

// Initialize navigation highlighting and toggle
document.addEventListener('DOMContentLoaded', () => {
  highlightCurrentPage();
  setupNavToggle();
});
