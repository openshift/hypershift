// Issues table page logic

let issuesData = [];
let sortColumn = null;
let sortDirection = 'asc';
let activeLoadId = 0;
let expandedGroups = new Set();

// Load issues from API
async function loadIssues(from, to) {
  const loadId = ++activeLoadId;
  try {
    const data = await fetchAPI(`/api/issues?from=${from}&to=${to}`);
    if (loadId !== activeLoadId) return;
    issuesData = data;
    updateComponentChips(extractComponents(data, i => i.component || 'hypershift'));
    updateResultsCount();
    renderIssuesTable();
  } catch (error) {
    if (loadId !== activeLoadId) return;
    showError('Failed to load issues: ' + error.message);
  }
}

function getFilteredIssues() {
  return filterByComponent(issuesData, i => i.component || 'hypershift');
}

function updateResultsCount() {
  const el = document.getElementById('results-count');
  const groups = groupByJiraKey(getFilteredIssues());
  const totalIssues = groups.length;
  const totalSessions = issuesData.length;
  const merged = groups.filter(g => g.best.pr_merged).length;
  const closed = groups.filter(g => g.best.pr_closed && !g.best.pr_merged).length;
  const open = totalIssues - merged - closed;
  el.textContent = `${totalIssues} issue${totalIssues !== 1 ? 's' : ''} (${totalSessions} sessions) — ${merged} merged, ${open} open, ${closed} closed`;
}

function renderIssuesTable() {
  const tbody = document.getElementById('issues-tbody');
  tbody.innerHTML = '';

  if (!issuesData || issuesData.length === 0) {
    tbody.innerHTML = '<tr><td colspan="17" style="text-align:center;">No issues found for this date range.</td></tr>';
    return;
  }

  const groups = groupByJiraKey(getFilteredIssues());

  groups.forEach(group => {
    const issue = group.best;
    const isExpanded = expandedGroups.has(group.jiraKey);
    const hasMultiple = group.count > 1;

    let statusClass = 'open';
    if (issue.pr_merged) {
      statusClass = 'merged';
    } else if (issue.pr_closed) {
      statusClass = 'closed';
    }

    const resolvedAt = issue.merged_at || issue.closed_at || '';

    const row = document.createElement('tr');
    row.classList.add('group-row');
    if (hasMultiple) row.classList.add('group-expandable');
    if (isExpanded) row.classList.add('group-expanded');

    const expandIcon = hasMultiple ? (isExpanded ? '▼' : '▶') : '';
    const expandCell = hasMultiple
      ? `<span class="group-toggle" title="Click to ${isExpanded ? 'collapse' : 'expand'} sessions">${expandIcon}</span> `
      : '';

    const prCell = issue.pr_number > 0
      ? `<a href="${escapeHTML(issue.pr_url)}" target="_blank" onclick="event.stopPropagation()">#${issue.pr_number}</a>`
      : '—';

    const prowJobCell = hasMultiple
      ? '<td></td>'
      : (issue.artifact_url
        ? `<td><a href="${escapeHTML(issue.artifact_url)}" target="_blank" onclick="event.stopPropagation()">logs</a></td>`
        : '<td></td>');

    const comp = issue.component || 'hypershift';
    row.innerHTML = `
      <td>${expandCell}<a href="${escapeHTML(issue.jira_url)}" target="_blank" onclick="event.stopPropagation()">${escapeHTML(issue.jira_key)}</a></td>
      <td><span class="badge component-${comp}">${comp}</span></td>
      ${prowJobCell}
      <td>${prCell}</td>
      <td><span class="badge ${statusClass}">${statusClass}</span></td>
      <td>${formatDate(issue.pr_created_at)}</td>
      <td>${formatDate(resolvedAt)}</td>
      <td>${formatNumber(issue.review_comment_count)}</td>
      <td>${formatNumber(issue.review_cycles)}</td>
      <td class="lines-added">+${formatNumber(issue.lines_added)}</td>
      <td class="lines-deleted">-${formatNumber(issue.lines_deleted)}</td>
      <td>${formatNumber(issue.files_changed)}</td>
      <td>${formatCost(group.totalCost)}</td>
      <td>${issue.quality_score != null ? issue.quality_score.toFixed(1) : 'N/A'}</td>
      <td>${formatDuration(issue.merge_duration)}</td>
      <td>${group.count > 1 ? group.count + ' runs' : '1 run'}</td>
      <td class="row-action"><span class="row-chevron" title="View issue details">→</span></td>
    `;

    row.addEventListener('click', (e) => {
      if (e.target.closest('.group-toggle')) {
        e.stopPropagation();
        if (expandedGroups.has(group.jiraKey)) {
          expandedGroups.delete(group.jiraKey);
        } else {
          expandedGroups.add(group.jiraKey);
        }
        renderIssuesTable();
        return;
      }
      window.location.href = `issue.html?id=${issue.id}`;
    });

    tbody.appendChild(row);

    if (isExpanded) {
      group.sessions.forEach((session, idx) => {
        const childRow = document.createElement('tr');
        childRow.classList.add('group-child');
        childRow.dataset.issueId = session.id;

        let childStatus = 'open';
        if (session.pr_merged) childStatus = 'merged';
        else if (session.pr_closed) childStatus = 'closed';

        const childResolved = session.merged_at || session.closed_at || '';
        const childPrCell = session.pr_number > 0
          ? `<a href="${escapeHTML(session.pr_url)}" target="_blank" onclick="event.stopPropagation()">#${session.pr_number}</a>`
          : '—';
        const childProwCell = session.artifact_url
          ? `<td><a href="${escapeHTML(session.artifact_url)}" target="_blank" onclick="event.stopPropagation()">logs</a></td>`
          : '<td></td>';

        const connector = idx < group.sessions.length - 1 ? '├─' : '└─';

        const childComp = session.component || 'hypershift';
        childRow.innerHTML = `
          <td class="child-label"><span class="child-connector">${connector}</span> session ${idx + 1}</td>
          <td><span class="badge component-${childComp}">${childComp}</span></td>
          ${childProwCell}
          <td>${childPrCell}</td>
          <td><span class="badge ${childStatus}">${childStatus}</span></td>
          <td>${formatDate(session.pr_created_at)}</td>
          <td>${formatDate(childResolved)}</td>
          <td>${formatNumber(session.review_comment_count)}</td>
          <td>${formatNumber(session.review_cycles)}</td>
          <td class="lines-added">+${formatNumber(session.lines_added)}</td>
          <td class="lines-deleted">-${formatNumber(session.lines_deleted)}</td>
          <td>${formatNumber(session.files_changed)}</td>
          <td>${formatCost(session.total_cost)}</td>
          <td>${session.quality_score != null ? session.quality_score.toFixed(1) : 'N/A'}</td>
          <td>${formatDuration(session.merge_duration)}</td>
          <td></td>
          <td class="row-action"><span class="row-chevron" title="View session details">→</span></td>
        `;

        childRow.addEventListener('click', () => {
          window.location.href = `issue.html?id=${session.id}`;
        });

        tbody.appendChild(childRow);
      });
    }
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

  document.querySelector('[data-column="component"]').addEventListener('click', () => {
    sortTable('component', issue => issue.component || 'hypershift');
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

  document.querySelector('[data-column="review_cycles"]').addEventListener('click', () => {
    sortTable('review_cycles', issue => issue.review_cycles || 0);
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

  document.querySelector('[data-column="runs"]').addEventListener('click', () => {
    sortTable('runs', issue => issue.jira_key);
  });
}

document.addEventListener('DOMContentLoaded', () => {
  initComponentFilter(() => {
    updateResultsCount();
    renderIssuesTable();
  });
  initTimeRange(loadIssues);
  setupSortHandlers();

  document.querySelectorAll('.info-tip').forEach(tip => {
    tip.addEventListener('click', e => e.stopPropagation());
  });
});
