// Issues table page logic

let issuesData = [];
let sortColumn = null;
let sortDirection = 'asc';
let activeLoadId = 0;

// Load issues from API
async function loadIssues(from, to) {
  const loadId = ++activeLoadId;
  try {
    const data = await fetchAPI(`/api/issues?from=${from}&to=${to}`);
    if (loadId !== activeLoadId) return;
    issuesData = data;
    updateResultsCount();
    renderIssuesTable();
  } catch (error) {
    if (loadId !== activeLoadId) return;
    showError('Failed to load issues: ' + error.message);
  }
}

function updateResultsCount() {
  const el = document.getElementById('results-count');
  const total = issuesData.length;
  const merged = issuesData.filter(i => i.pr_merged).length;
  const closed = issuesData.filter(i => i.pr_closed).length;
  const open = total - merged - closed;
  el.textContent = `${total} issue${total !== 1 ? 's' : ''} found — ${merged} merged, ${open} open, ${closed} closed`;
}

// Render issues table
function renderIssuesTable() {
  const tbody = document.getElementById('issues-tbody');
  tbody.innerHTML = '';

  if (!issuesData || issuesData.length === 0) {
    tbody.innerHTML = '<tr><td colspan="12" style="text-align:center;">No issues found for this date range.</td></tr>';
    return;
  }

  issuesData.forEach(issue => {
    const row = document.createElement('tr');
    row.dataset.issueId = issue.id;
    row.addEventListener('click', () => {
      window.location.href = `issue.html?id=${issue.id}`;
    });

    // Build status badge
    let statusClass = 'open';
    if (issue.pr_merged) {
      statusClass = 'merged';
    } else if (issue.pr_closed) {
      statusClass = 'closed';
    }

    // Determine resolved date (merged_at or closed_at)
    const resolvedAt = issue.merged_at || issue.closed_at || '';

    row.innerHTML = `
      <td><a href="${escapeHTML(issue.jira_url)}" target="_blank" onclick="event.stopPropagation()">${escapeHTML(issue.jira_key)}</a></td>
      <td><a href="${escapeHTML(issue.pr_url)}" target="_blank" onclick="event.stopPropagation()">#${issue.pr_number}</a></td>
      <td><span class="badge ${statusClass}">${statusClass}</span></td>
      <td>${formatDate(issue.pr_created_at)}</td>
      <td>${formatDate(resolvedAt)}</td>
      <td>${formatNumber(issue.review_comment_count)}</td>
      <td class="lines-added">+${formatNumber(issue.lines_added)}</td>
      <td class="lines-deleted">-${formatNumber(issue.lines_deleted)}</td>
      <td>${formatNumber(issue.files_changed)}</td>
      <td>${formatCost(issue.total_cost)}</td>
      <td>${issue.quality_score != null ? issue.quality_score.toFixed(1) : 'N/A'}</td>
      <td>${formatDuration(issue.merge_duration)}</td>
      <td class="row-action"><span class="row-chevron" title="View issue details">→</span></td>
    `;

    tbody.appendChild(row);
  });
}

// Sort table by column
function sortTable(column, getValue) {
  const currentDirection = sortColumn === column ? sortDirection : 'asc';
  const newDirection = currentDirection === 'asc' ? 'desc' : 'asc';

  issuesData.sort((a, b) => {
    const valA = getValue(a);
    const valB = getValue(b);

    if (valA == null && valB == null) return 0;
    if (valA == null) return 1;
    if (valB == null) return -1;

    if (valA < valB) return newDirection === 'asc' ? -1 : 1;
    if (valA > valB) return newDirection === 'asc' ? 1 : -1;
    return 0;
  });

  sortColumn = column;
  sortDirection = newDirection;

  // Update table headers
  document.querySelectorAll('th.sortable').forEach(th => {
    th.classList.remove('sort-asc', 'sort-desc');
  });
  const headerElement = document.querySelector(`th[data-column="${column}"]`);
  if (headerElement) {
    headerElement.classList.add(`sort-${newDirection}`);
  }

  renderIssuesTable();
}

// Setup column sort handlers
function setupSortHandlers() {
  document.querySelector('[data-column="jira_key"]').addEventListener('click', () => {
    sortTable('jira_key', issue => issue.jira_key);
  });

  document.querySelector('[data-column="pr_number"]').addEventListener('click', () => {
    sortTable('pr_number', issue => issue.pr_number);
  });

  document.querySelector('[data-column="status"]').addEventListener('click', () => {
    sortTable('status', issue => {
      if (issue.pr_merged) return 2;
      if (issue.pr_closed) return 1;
      return 0;
    });
  });

  document.querySelector('[data-column="pr_created_at"]').addEventListener('click', () => {
    sortTable('pr_created_at', issue => issue.pr_created_at || '');
  });

  document.querySelector('[data-column="resolved_at"]').addEventListener('click', () => {
    sortTable('resolved_at', issue => issue.merged_at || issue.closed_at || '');
  });

  document.querySelector('[data-column="review_comments"]').addEventListener('click', () => {
    sortTable('review_comments', issue => issue.review_comment_count || 0);
  });

  document.querySelector('[data-column="lines_added"]').addEventListener('click', () => {
    sortTable('lines_added', issue => issue.lines_added || 0);
  });

  document.querySelector('[data-column="lines_deleted"]').addEventListener('click', () => {
    sortTable('lines_deleted', issue => issue.lines_deleted || 0);
  });

  document.querySelector('[data-column="files_changed"]').addEventListener('click', () => {
    sortTable('files_changed', issue => issue.files_changed || 0);
  });

  document.querySelector('[data-column="cost"]').addEventListener('click', () => {
    sortTable('cost', issue => issue.total_cost || 0);
  });

  document.querySelector('[data-column="quality_score"]').addEventListener('click', () => {
    sortTable('quality_score', issue => issue.quality_score || 0);
  });

  document.querySelector('[data-column="duration"]').addEventListener('click', () => {
    sortTable('duration', issue => issue.merge_duration || 0);
  });
}

// --- Time Range Helpers (same as overview) ---

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

  document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
  document.getElementById('range-' + rangeKey).classList.add('active');

  document.getElementById('date-from').value = from;
  document.getElementById('date-to').value = to;

  loadIssues(from, to);
}

function applyCustomDateRange() {
  const from = document.getElementById('date-from').value;
  const to = document.getElementById('date-to').value;

  if (from && to) {
    document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
    loadIssues(from, to);
  }
}

function setupTimeRangeHandlers() {
  document.getElementById('range-7d').addEventListener('click', () => applyRange('7d'));
  document.getElementById('range-this-month').addEventListener('click', () => applyRange('this-month'));
  document.getElementById('range-last-month').addEventListener('click', () => applyRange('last-month'));
  document.getElementById('range-3m').addEventListener('click', () => applyRange('3m'));
  document.getElementById('range-ytd').addEventListener('click', () => applyRange('ytd'));

  document.getElementById('date-from').addEventListener('change', applyCustomDateRange);
  document.getElementById('date-to').addEventListener('change', applyCustomDateRange);
}

// Initialize on page load — default to last 7 days
document.addEventListener('DOMContentLoaded', () => {
  setupTimeRangeHandlers();
  setupSortHandlers();

  // Prevent info-tip clicks from triggering column sort
  document.querySelectorAll('.info-tip').forEach(tip => {
    tip.addEventListener('click', e => e.stopPropagation());
  });

  applyRange('7d');
});
