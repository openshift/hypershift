// Issues table page logic

let issuesData = [];
let sortColumn = null;
let sortDirection = 'asc';

// Load issues from API
async function loadIssues(from, to) {
  try {
    issuesData = await fetchAPI(`/api/issues?from=${from}&to=${to}`);
    renderIssuesTable();
  } catch (error) {
    showError('Failed to load issues: ' + error.message);
  }
}

// Render issues table
function renderIssuesTable() {
  const tbody = document.getElementById('issues-tbody');
  tbody.innerHTML = '';

  if (!issuesData || issuesData.length === 0) {
    tbody.innerHTML = '<tr><td colspan="10" style="text-align:center;">No issues found for this date range.</td></tr>';
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

    row.innerHTML = `
      <td><a href="${issue.jira_url}" target="_blank" onclick="event.stopPropagation()">${issue.jira_key}</a></td>
      <td><a href="${issue.pr_url}" target="_blank" onclick="event.stopPropagation()">#${issue.pr_number}</a></td>
      <td><span class="badge ${statusClass}">${statusClass}</span></td>
      <td>${formatNumber(issue.review_comment_count)}</td>
      <td>${formatNumber(issue.lines_changed)}</td>
      <td>${formatNumber(issue.files_changed)}</td>
      <td>${formatNumber(issue.complexity_delta)}</td>
      <td>${formatCost(issue.total_cost)}</td>
      <td>${formatDuration(issue.merge_duration)}</td>
      <td>${issue.quality_score != null ? issue.quality_score.toFixed(1) : 'N/A'}</td>
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

  document.querySelector('[data-column="review_comments"]').addEventListener('click', () => {
    sortTable('review_comments', issue => issue.review_comment_count || 0);
  });

  document.querySelector('[data-column="lines_changed"]').addEventListener('click', () => {
    sortTable('lines_changed', issue => issue.lines_changed || 0);
  });

  document.querySelector('[data-column="files_changed"]').addEventListener('click', () => {
    sortTable('files_changed', issue => issue.files_changed || 0);
  });

  document.querySelector('[data-column="complexity_delta"]').addEventListener('click', () => {
    sortTable('complexity_delta', issue => issue.complexity_delta || 0);
  });

  document.querySelector('[data-column="cost"]').addEventListener('click', () => {
    sortTable('cost', issue => issue.total_cost || 0);
  });

  document.querySelector('[data-column="duration"]').addEventListener('click', () => {
    sortTable('duration', issue => issue.merge_duration || 0);
  });

  document.querySelector('[data-column="quality_score"]').addEventListener('click', () => {
    sortTable('quality_score', issue => issue.quality_score || 0);
  });
}

// Time range button handlers
function setupTimeRangeHandlers() {
  document.getElementById('range-30d').addEventListener('click', () => {
    const to = new Date();
    const from = new Date();
    from.setDate(to.getDate() - 30);
    applyDateRange(from, to);
  });

  document.getElementById('range-90d').addEventListener('click', () => {
    const to = new Date();
    const from = new Date();
    from.setDate(to.getDate() - 90);
    applyDateRange(from, to);
  });

  document.getElementById('date-from').addEventListener('change', applyCustomDateRange);
  document.getElementById('date-to').addEventListener('change', applyCustomDateRange);
}

function applyDateRange(from, to) {
  const fromStr = from.toISOString().split('T')[0];
  const toStr = to.toISOString().split('T')[0];

  setDateRange(fromStr, toStr);
  document.getElementById('date-from').value = fromStr;
  document.getElementById('date-to').value = toStr;
  updateActiveButton(null);
  loadIssues(fromStr, toStr);
}

function applyCustomDateRange() {
  const from = document.getElementById('date-from').value;
  const to = document.getElementById('date-to').value;

  if (from && to) {
    setDateRange(from, to);
    updateActiveButton(null);
    loadIssues(from, to);
  }
}

function updateActiveButton(buttonId) {
  document.querySelectorAll('.time-range button').forEach(btn => {
    btn.classList.remove('active');
  });
  if (buttonId) {
    document.getElementById(buttonId).classList.add('active');
  }
}

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
  const { from, to } = getDateRange();

  document.getElementById('date-from').value = from;
  document.getElementById('date-to').value = to;

  const today = new Date().toISOString().split('T')[0];
  const date90 = new Date();
  date90.setDate(date90.getDate() - 90);
  const date30 = new Date();
  date30.setDate(date30.getDate() - 30);

  if (from === date90.toISOString().split('T')[0] && to === today) {
    updateActiveButton('range-90d');
  } else if (from === date30.toISOString().split('T')[0] && to === today) {
    updateActiveButton('range-30d');
  }

  setupTimeRangeHandlers();
  setupSortHandlers();
  loadIssues(from, to);
});
