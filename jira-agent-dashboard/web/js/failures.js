// Failure Analysis page logic

let chartsInstances = {};

async function loadFailures(from, to) {
  try {
    const [issues, comments] = await Promise.all([
      fetchAPI(`/api/issues?from=${from}&to=${to}`),
      fetchAPI(`/api/comments/summary?from=${from}&to=${to}`)
    ]);

    const issueMap = buildIssueMap(issues);

    const groups = groupByJiraKey(issues);
    updateSummaryCards(groups);
    renderRejectionReasonsChart(comments, issueMap);
    renderTopicOutcomeChart(comments, issueMap);
    renderRejectedTable(groups, comments, issueMap);
  } catch (error) {
    showError('Failed to load failure analysis data: ' + error.message);
  }
}

// --- Summary Cards ---

function updateSummaryCards(groups) {
  const total = groups.length;
  const rejected = groups.filter(g => g.best.pr_closed && !g.best.pr_merged);
  const rejectedCount = rejected.length;
  const rejectionRate = total > 0 ? (rejectedCount / total * 100) : 0;
  const wastedCost = rejected.reduce((sum, g) => sum + g.totalCost, 0);

  document.getElementById('rejected-count').textContent = formatNumber(rejectedCount);
  document.getElementById('rejection-rate').textContent = total > 0 ? rejectionRate.toFixed(0) + '%' : 'N/A';
  document.getElementById('wasted-cost').textContent = formatCost(wastedCost);
}

// --- Charts ---

function destroyChart(key) {
  if (chartsInstances[key]) {
    chartsInstances[key].destroy();
    chartsInstances[key] = null;
  }
}

function renderRejectionReasonsChart(comments, issueMap) {
  const ctx = document.getElementById('rejection-reasons-chart').getContext('2d');
  destroyChart('rejectionReasons');

  const topicCounts = {};
  (comments || []).forEach(c => {
    const info = issueMap[c.issue_id];
    const isRejected = info && info.closed && !info.merged;
    if (isRejected && c.severity === 'required_change' && c.topic) {
      topicCounts[c.topic] = (topicCounts[c.topic] || 0) + 1;
    }
  });

  const sorted = Object.entries(topicCounts)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 10);

  chartsInstances.rejectionReasons = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: sorted.map(e => e[0].replace(/_/g, ' ')),
      datasets: [{
        label: 'Count',
        data: sorted.map(e => e[1]),
        backgroundColor: '#e74c3c',
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

function renderTopicOutcomeChart(comments, issueMap) {
  const ctx = document.getElementById('topic-outcome-chart').getContext('2d');
  destroyChart('topicOutcome');

  const topicMerged = {};
  const topicClosed = {};
  (comments || []).forEach(c => {
    if (!c.topic) return;
    const info = issueMap[c.issue_id];
    if (!info) return;
    if (info.merged) {
      topicMerged[c.topic] = (topicMerged[c.topic] || 0) + 1;
    } else if (info.closed) {
      topicClosed[c.topic] = (topicClosed[c.topic] || 0) + 1;
    }
  });

  const allTopics = new Set([...Object.keys(topicMerged), ...Object.keys(topicClosed)]);
  const sorted = [...allTopics]
    .map(t => ({ topic: t, total: (topicMerged[t] || 0) + (topicClosed[t] || 0) }))
    .sort((a, b) => b.total - a.total)
    .slice(0, 10);

  chartsInstances.topicOutcome = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: sorted.map(e => e.topic.replace(/_/g, ' ')),
      datasets: [
        {
          label: 'Merged PRs',
          data: sorted.map(e => topicMerged[e.topic] || 0),
          backgroundColor: '#27ae60',
          borderRadius: 4
        },
        {
          label: 'Closed PRs',
          data: sorted.map(e => topicClosed[e.topic] || 0),
          backgroundColor: '#e74c3c',
          borderRadius: 4
        }
      ]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { position: 'bottom' } },
      scales: {
        x: { beginAtZero: true, stacked: false, ticks: { stepSize: 1 } }
      }
    }
  });
}

// --- Rejected PRs Table ---

function renderRejectedTable(groups, comments, issueMap) {
  const tbody = document.getElementById('rejected-tbody');

  const rejected = groups
    .filter(g => g.best.pr_closed && !g.best.pr_merged)
    .sort((a, b) => b.totalCost - a.totalCost);

  if (rejected.length === 0) {
    tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--text-secondary);padding:40px">No rejected PRs in this time range.</td></tr>';
    return;
  }

  const commentsByJiraKey = {};
  (comments || []).forEach(c => {
    const info = issueMap[c.issue_id];
    if (!info) return;
    const key = info.jiraKey;
    if (!commentsByJiraKey[key]) commentsByJiraKey[key] = [];
    commentsByJiraKey[key].push(c);
  });

  tbody.innerHTML = rejected.map(g => {
    const issue = g.best;
    const issueComments = commentsByJiraKey[g.jiraKey] || [];
    const commentCount = issueComments.length;
    const topIssues = [...new Set(
      issueComments
        .filter(c => c.severity === 'required_change' && c.topic)
        .map(c => c.topic)
    )];

    const prLink = issue.pr_url
      ? `<a href="${escapeHTML(issue.pr_url)}" target="_blank">#${issue.pr_number || 'PR'}</a>`
      : 'N/A';

    const qualityScore = issue.quality_score != null
      ? formatNumber(issue.quality_score)
      : 'N/A';

    const topIssuesHtml = topIssues.length > 0
      ? topIssues.map(t => `<span class="tag ${escapeHTML(t)}">${escapeHTML(t.replace(/_/g, ' '))}</span>`).join(' ')
      : '<span style="color:var(--text-secondary)">—</span>';

    return `<tr>
      <td><a href="issue.html?id=${issue.id}">${escapeHTML(g.jiraKey)}</a></td>
      <td>${prLink}</td>
      <td>${formatCost(g.totalCost)}</td>
      <td>${qualityScore}</td>
      <td>${formatNumber(commentCount)}</td>
      <td>${topIssuesHtml}</td>
    </tr>`;
  }).join('');
}

document.addEventListener('DOMContentLoaded', () => {
  initTimeRange(loadFailures);
});
