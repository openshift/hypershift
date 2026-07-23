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

    localStorage.setItem('dashboard-time-range', rangeKey);
    localStorage.setItem('dashboard-date-from', from);
    localStorage.setItem('dashboard-date-to', to);

    loadFn(from, to);
  }

  function applyCustomDateRange() {
    const from = document.getElementById('date-from').value;
    const to = document.getElementById('date-to').value;
    if (from && to) {
      document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
      localStorage.setItem('dashboard-time-range', 'custom');
      localStorage.setItem('dashboard-date-from', from);
      localStorage.setItem('dashboard-date-to', to);
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

  const savedRange = localStorage.getItem('dashboard-time-range');
  if (savedRange && savedRange !== 'custom' && getTimeRanges()[savedRange]) {
    applyRange(savedRange);
  } else if (savedRange === 'custom') {
    const from = localStorage.getItem('dashboard-date-from');
    const to = localStorage.getItem('dashboard-date-to');
    if (from && to) {
      document.getElementById('date-from').value = from;
      document.getElementById('date-to').value = to;
      document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
      loadFn(from, to);
    } else {
      applyRange('7d');
    }
  } else {
    applyRange('7d');
  }
}

// Build a map of issue.id → {merged, closed, jiraKey, component} for outcome cross-referencing
function buildIssueMap(issues) {
  const map = {};
  for (const issue of issues) {
    map[issue.id] = {
      merged: issue.pr_merged,
      closed: issue.pr_closed,
      jiraKey: issue.jira_key,
      component: issue.component || 'hypershift',
    };
  }
  return map;
}

// Build a map of jiraKey → component for telemetry cross-referencing
function buildKeyComponentMap(issues) {
  const map = {};
  for (const issue of issues) {
    map[issue.jira_key] = issue.component || 'hypershift';
  }
  return map;
}

// --- Component Filter ---

const _componentState = {
  active: new Set(),
  all: new Set(),
  reloadFn: null,
};

const COMPONENT_COLORS = {
  hypershift: { bg: '#cce5ff', color: '#004085' },
  installer:  { bg: '#e8daef', color: '#4a235a' },
};

function _defaultColor(name) {
  const hue = [...name].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 0);
  return { bg: `hsl(${hue}, 40%, 90%)`, color: `hsl(${hue}, 50%, 25%)` };
}

function initComponentFilter(reloadFn) {
  _componentState.reloadFn = reloadFn;
  const stored = localStorage.getItem('dashboard-components-active');
  if (stored) {
    try {
      _componentState._pendingRestore = new Set(JSON.parse(stored));
    } catch (e) { /* ignore bad data */ }
  }
}

function updateComponentChips(components) {
  const container = document.getElementById('component-filter-container');
  if (!container) return;

  const newAll = new Set(components);
  if (_componentState.all.size === 0) {
    if (_componentState._pendingRestore) {
      _componentState.active = new Set(
        [...newAll].filter(c => _componentState._pendingRestore.has(c))
      );
      _componentState._pendingRestore = null;
    } else {
      _componentState.active = new Set(newAll);
    }
  } else {
    for (const c of newAll) {
      if (!_componentState.all.has(c)) _componentState.active.add(c);
    }
    for (const c of _componentState.active) {
      if (!newAll.has(c)) _componentState.active.delete(c);
    }
  }
  _componentState.all = newAll;

  container.innerHTML = '';

  if (newAll.size <= 1) return;

  const allActive = _componentState.active.size === _componentState.all.size;
  const toggleAll = document.createElement('button');
  toggleAll.className = 'component-chip' + (allActive ? ' all-active' : '');
  toggleAll.textContent = 'All';
  toggleAll.addEventListener('click', () => {
    if (_componentState.active.size === _componentState.all.size) {
      _componentState.active.clear();
    } else {
      _componentState.active = new Set(_componentState.all);
    }
    updateComponentChips([..._componentState.all]);
    if (_componentState.reloadFn) _componentState.reloadFn();
  });
  container.appendChild(toggleAll);

  for (const name of [...newAll].sort()) {
    const active = _componentState.active.has(name);
    const colors = COMPONENT_COLORS[name] || _defaultColor(name);

    const chip = document.createElement('button');
    chip.className = 'component-chip' + (active ? ' active' : ' inactive');
    chip.textContent = name;
    if (active) {
      chip.style.background = colors.bg;
      chip.style.color = colors.color;
    }
    chip.addEventListener('click', () => {
      if (_componentState.active.has(name)) {
        _componentState.active.delete(name);
      } else {
        _componentState.active.add(name);
      }
      updateComponentChips([..._componentState.all]);
      if (_componentState.reloadFn) _componentState.reloadFn();
    });
    container.appendChild(chip);
  }

  localStorage.setItem('dashboard-components-active', JSON.stringify([..._componentState.active]));
}

function getActiveComponents() {
  if (_componentState.active.size === _componentState.all.size) {
    return null;
  }
  return _componentState.active;
}

function filterByComponent(items, getComp) {
  const active = getActiveComponents();
  if (!active) return items;
  return items.filter(item => active.has(getComp(item)));
}

function filterCommentsByIssueMap(comments, issueMap) {
  const active = getActiveComponents();
  if (!active) return comments;
  return comments.filter(c => {
    const info = issueMap[c.issue_id];
    return info && active.has(info.component);
  });
}

function extractComponents(items, getComp) {
  const set = new Set();
  for (const item of items) {
    set.add(getComp(item));
  }
  return [...set];
}

// Safely destroy and nullify a Chart.js instance
function resetChart(chart) {
  if (chart) chart.destroy();
  return null;
}

// --- Header Bar Injection ---

function buildHeaderBar() {
  const bar = document.createElement('div');
  bar.className = 'time-range';
  bar.innerHTML = `
    <label>Time Range:</label>
    <button id="range-7d" class="active">Last 7 Days</button>
    <button id="range-this-month">This Month</button>
    <button id="range-last-month">Last Month</button>
    <button id="range-3m">Last 90 Days</button>
    <button id="range-ytd">Year to Date</button>
    <span>|</span>
    <label for="date-from">From:</label>
    <input type="date" id="date-from">
    <label for="date-to">To:</label>
    <input type="date" id="date-to">
    <span>|</span>
    <div id="component-filter-container"></div>
  `;
  return bar;
}

function injectHeaderBar() {
  const anchor = document.getElementById('header-bar-anchor');
  if (!anchor) return;
  anchor.appendChild(buildHeaderBar());
}

// Initialize navigation highlighting, toggle, and header bar
document.addEventListener('DOMContentLoaded', () => {
  injectHeaderBar();
  highlightCurrentPage();
  setupNavToggle();
});
