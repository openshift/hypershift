// Comments classification summary page logic

let severityChart = null;
let topicChart = null;
let topicMergeRateChart = null;
let allComments = []; // stored for filtering
let filteredComments = []; // current filtered view
let issueMap = {};
let _reviewIssues = [];

// Load comments data and render charts
async function loadComments(from, to) {
  try {
    const [comments, issues] = await Promise.all([
      fetchAPI(`/api/comments/summary?from=${from}&to=${to}`),
      fetchAPI(`/api/issues?from=${from}&to=${to}`)
    ]);

    allComments = comments;
    _reviewIssues = issues;
    issueMap = buildIssueMap(issues);
    updateComponentChips(extractComponents(issues, i => i.component || 'hypershift'));
    renderReviews();
  } catch (error) {
    showError('Failed to load comments data: ' + error.message);
  }
}

function renderReviews() {
  const filteredIssues = filterByComponent(_reviewIssues, i => i.component || 'hypershift');
  issueMap = buildIssueMap(filteredIssues);
  const comments = filterCommentsByIssueMap(allComments, buildIssueMap(_reviewIssues));

  renderOutcomeSummaryCards(comments, issueMap);
  renderTopicMergeRateChart(comments, issueMap);
  renderSeverityChart(comments);
  renderTopicChart(comments);
  renderPatternTable(comments, issueMap);
  populateAuthorFilter(comments);
  applyCommentFilters();
}

function renderOutcomeSummaryCards(comments, issueMap) {
  let onMerged = 0;
  let onClosed = 0;
  (comments || []).forEach(c => {
    const info = issueMap[c.issue_id];
    if (!info) return;
    if (info.merged) onMerged++;
    else if (info.closed) onClosed++;
  });

  document.getElementById('comments-merged').textContent = formatNumber(onMerged);
  document.getElementById('comments-closed').textContent = formatNumber(onClosed);

  const topicRates = getTopicMergeRates(comments, issueMap);
  const rates = Object.values(topicRates).map(t => t.rate);
  const avgRate = rates.length > 0 ? rates.reduce((a, b) => a + b, 0) / rates.length : 0;
  document.getElementById('avg-topic-merge-rate').textContent = avgRate.toFixed(0) + '%';
}

function getTopicMergeRates(comments, issueMap) {
  const topicMerged = {};
  const topicTotal = {};
  (comments || []).forEach(c => {
    if (!c.topic) return;
    const info = issueMap[c.issue_id];
    if (!info) return;
    if (!info.merged && !info.closed) return;
    topicTotal[c.topic] = (topicTotal[c.topic] || 0) + 1;
    if (info.merged) {
      topicMerged[c.topic] = (topicMerged[c.topic] || 0) + 1;
    }
  });
  const result = {};
  for (const topic of Object.keys(topicTotal)) {
    const merged = topicMerged[topic] || 0;
    const total = topicTotal[topic];
    result[topic] = { merged, total, rate: total > 0 ? (merged / total * 100) : 0 };
  }
  return result;
}

function renderTopicMergeRateChart(comments, issueMap) {
  const ctx = document.getElementById('topic-merge-rate-chart').getContext('2d');
  topicMergeRateChart = resetChart(topicMergeRateChart);

  const rates = getTopicMergeRates(comments, issueMap);
  const sorted = Object.entries(rates)
    .filter(([, v]) => v.total >= 2)
    .sort((a, b) => a[1].rate - b[1].rate);

  if (sorted.length === 0) {
    topicMergeRateChart = null;
    ctx.canvas.parentElement.style.display = 'none';
    return;
  }
  ctx.canvas.parentElement.style.display = '';

  topicMergeRateChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: sorted.map(([t]) => t.replace(/_/g, ' ')),
      datasets: [{
        label: 'Merge Rate %',
        data: sorted.map(([, v]) => v.rate),
        backgroundColor: sorted.map(([, v]) => v.rate >= 50 ? '#27ae60' : '#e74c3c'),
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
            label: (ctx) => {
              const entry = sorted[ctx.dataIndex];
              return `${entry[1].rate.toFixed(0)}% (${entry[1].merged}/${entry[1].total})`;
            }
          }
        }
      },
      scales: {
        x: { beginAtZero: true, max: 100, ticks: { callback: v => v + '%' } }
      }
    }
  });
}

// Populate author dropdown from loaded comments
function populateAuthorFilter(comments) {
  const select = document.getElementById('filter-author');
  const current = select.value;
  const authors = [...new Set(comments.map(c => c.author))].sort();
  select.innerHTML = '<option value="">All Authors</option>';
  authors.forEach(a => {
    const opt = document.createElement('option');
    opt.value = a;
    opt.textContent = a;
    select.appendChild(opt);
  });
  select.value = current;
}

// Filter and re-render comment list
function applyCommentFilters() {
  const severity = document.getElementById('filter-severity').value;
  const topic = document.getElementById('filter-topic').value;
  const author = document.getElementById('filter-author').value;
  const search = document.getElementById('filter-search').value.toLowerCase().trim();

  const componentFiltered = filterCommentsByIssueMap(allComments, buildIssueMap(_reviewIssues));
  const filtered = componentFiltered.filter(c => {
    if (severity && (c.severity || 'unclassified') !== severity) return false;
    if (topic && (c.topic || 'unclassified') !== topic) return false;
    if (author && c.author !== author) return false;
    if (search && !(c.body || '').toLowerCase().includes(search) && !(c.author || '').toLowerCase().includes(search)) return false;
    return true;
  });

  filteredComments = filtered;
  document.getElementById('comment-count').textContent = `${filtered.length} of ${componentFiltered.length} comments`;
  renderCommentList(filtered);
}

// Render severity breakdown chart
function renderSeverityChart(comments) {
  const ctx = document.getElementById('severity-chart').getContext('2d');

  severityChart = resetChart(severityChart);

  const severityCounts = {
    'nitpick': 0,
    'suggestion': 0,
    'required_change': 0,
    'question': 0,
    'unclassified': 0
  };

  comments.forEach(comment => {
    const severity = comment.severity || 'unclassified';
    if (severityCounts.hasOwnProperty(severity)) {
      severityCounts[severity]++;
    } else {
      severityCounts.unclassified++;
    }
  });

  const labels = Object.keys(severityCounts).map(s => s.replace(/_/g, ' ').toUpperCase());
  const data = Object.values(severityCounts);
  const backgroundColors = [
    '#3498db', // nitpick - blue
    '#f39c12', // suggestion - orange
    '#e74c3c', // required_change - red
    '#9b59b6', // question - purple
    '#95a5a6'  // unclassified - gray
  ];

  severityChart = new Chart(ctx, {
    type: 'doughnut',
    data: {
      labels: labels,
      datasets: [{
        data: data,
        backgroundColor: backgroundColors
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          position: 'bottom'
        }
      }
    }
  });
}

// Render topic breakdown chart
function renderTopicChart(comments) {
  const ctx = document.getElementById('topic-chart').getContext('2d');

  topicChart = resetChart(topicChart);

  const topicCounts = {
    'style': 0,
    'logic_bug': 0,
    'test_gap': 0,
    'api_design': 0,
    'architecture_design': 0,
    'security': 0,
    'documentation': 0,
    'ci': 0,
    'approval': 0,
    'process': 0,
    'unclassified': 0
  };

  comments.forEach(comment => {
    const topic = comment.topic || 'unclassified';
    if (topicCounts.hasOwnProperty(topic)) {
      topicCounts[topic]++;
    } else {
      topicCounts.unclassified++;
    }
  });

  // Filter out topics with zero count so the chart isn't cluttered
  const filtered = Object.entries(topicCounts).filter(([, count]) => count > 0);
  const labels = filtered.map(([t]) => t.replace(/_/g, ' ').toUpperCase());
  const data = filtered.map(([, count]) => count);

  const colorMap = {
    'style':               '#27ae60',
    'logic_bug':           '#e74c3c',
    'test_gap':            '#f1c40f',
    'api_design':          '#3498db',
    'architecture_design': '#5c6bc0',
    'security':            '#d32f2f',
    'documentation':       '#2ecc71',
    'ci':                  '#e67e22',
    'approval':            '#1abc9c',
    'process':             '#34495e',
    'unclassified':        '#95a5a6'
  };
  const backgroundColors = filtered.map(([t]) => colorMap[t] || '#95a5a6');

  topicChart = new Chart(ctx, {
    type: 'doughnut',
    data: {
      labels: labels,
      datasets: [{
        data: data,
        backgroundColor: backgroundColors
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          position: 'bottom'
        }
      }
    }
  });
}

// Render pattern table (severity + topic combinations with merge rate)
function renderPatternTable(comments, issueMap) {
  const tbody = document.getElementById('pattern-tbody');
  tbody.innerHTML = '';

  const patterns = {};

  comments.forEach(comment => {
    const severity = comment.severity || 'unclassified';
    const topic = comment.topic || 'unclassified';
    const key = `${severity}|${topic}`;

    if (!patterns[key]) {
      patterns[key] = {
        severity,
        topic,
        count: 0,
        merged: 0,
        resolved: 0
      };
    }
    patterns[key].count++;
    const info = issueMap[comment.issue_id];
    if (info && (info.merged || info.closed)) {
      patterns[key].resolved++;
      if (info.merged) patterns[key].merged++;
    }
  });

  const patternArray = Object.values(patterns).sort((a, b) => b.count - a.count);

  if (patternArray.length === 0) {
    tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;">No comment patterns found.</td></tr>';
    return;
  }

  patternArray.forEach(pattern => {
    const row = document.createElement('tr');
    const mergeRate = pattern.resolved > 0
      ? (pattern.merged / pattern.resolved * 100).toFixed(0) + '%'
      : '—';
    const rateClass = pattern.resolved > 0
      ? (pattern.merged / pattern.resolved >= 0.5 ? 'color: #27ae60' : 'color: #e74c3c')
      : '';
    row.innerHTML = `
      <td><span class="tag ${pattern.severity}">${pattern.severity.replace(/_/g, ' ')}</span></td>
      <td><span class="tag ${pattern.topic}">${pattern.topic.replace(/_/g, ' ')}</span></td>
      <td>${formatNumber(pattern.count)}</td>
      <td style="${rateClass}">${mergeRate}</td>
    `;
    tbody.appendChild(row);
  });
}

// Render individual comment list with collapsible bodies
function renderCommentList(comments) {
  const container = document.getElementById('comment-list');
  container.innerHTML = '';

  if (comments.length === 0) {
    container.innerHTML = '<p style="text-align:center; color: var(--text-secondary); padding: 20px;">No comments found.</p>';
    return;
  }

  comments.forEach(comment => {
    const severity = comment.severity || 'unclassified';
    const topic = comment.topic || 'unclassified';
    const isLong = comment.body.length > 200;
    let prLink = '';
    if (comment.pr_url && comment.pr_url.startsWith('https://')) {
      prLink = `<a href="${escapeHTML(comment.pr_url)}" target="_blank" rel="noopener">${escapeHTML(comment.pr_url.replace('https://github.com/', ''))}</a>`;
    }

    const div = document.createElement('div');
    div.className = 'comment';

    const bodyDiv = document.createElement('div');
    bodyDiv.className = 'comment-body' + (isLong ? ' collapsed' : '');
    bodyDiv.textContent = comment.body;

    let toggleBtn = null;
    if (isLong) {
      toggleBtn = document.createElement('button');
      toggleBtn.className = 'comment-toggle';
      toggleBtn.textContent = 'Show more';
      toggleBtn.addEventListener('click', () => {
        const collapsed = bodyDiv.classList.toggle('collapsed');
        toggleBtn.textContent = collapsed ? 'Show more' : 'Show less';
      });
    }

    div.innerHTML = `
      <div class="comment-header">
        <span class="comment-author">${escapeHTML(comment.author)}</span>
        <span style="color: var(--text-secondary); font-size: 0.85em;">${formatDate(comment.created_at)}</span>
        ${prLink ? `<span style="font-size: 0.85em;">${prLink}</span>` : ''}
      </div>
    `;
    div.appendChild(bodyDiv);
    if (toggleBtn) div.appendChild(toggleBtn);

    const classDiv = document.createElement('div');
    classDiv.className = 'comment-classification';
    const confidenceHTML = comment.confidence != null
      ? `<span class="classification-label">Confidence:</span> <span class="tag confidence" title="Classification confidence">${(comment.confidence * 100).toFixed(0)}%</span>`
      : '';
    const editLink = comment.issue_id && !window.location.hostname.startsWith('dashboard-public')
      ? `<a href="issue.html?id=${comment.issue_id}" class="edit-classification-link" title="Edit classification on issue detail page">Edit</a>`
      : '';
    classDiv.innerHTML = `
      <span class="classification-label">Severity:</span> <span class="tag ${severity}">${severity.replace(/_/g, ' ')}</span>
      <span class="classification-label">Topic:</span> <span class="tag ${topic}">${topic.replace(/_/g, ' ')}</span>
      ${confidenceHTML}
      ${comment.ai_classified ? '<span style="font-size:0.75em; color: var(--text-secondary);">AI classified</span>' : ''}
      ${comment.human_override ? '<span style="font-size:0.75em; color: var(--accent-green);">Human override</span>' : ''}
      ${editLink}
    `;
    div.appendChild(classDiv);

    container.appendChild(div);
  });
}

// Generate markdown report from comments
function generateMarkdownReport(comments) {
  const from = document.getElementById('date-from').value;
  const to = document.getElementById('date-to').value;
  const severity = document.getElementById('filter-severity').value;
  const topic = document.getElementById('filter-topic').value;
  const author = document.getElementById('filter-author').value;

  let md = `# Review Comments Report\n\n`;
  md += `**Date Range:** ${from} to ${to}\n`;
  md += `**Comments:** ${comments.length}`;
  if (comments.length !== allComments.length) {
    md += ` of ${allComments.length} (filtered)`;
  }
  md += `\n`;
  if (severity) md += `**Severity Filter:** ${severity.replace(/_/g, ' ')}\n`;
  if (topic) md += `**Topic Filter:** ${topic.replace(/_/g, ' ')}\n`;
  if (author) md += `**Author Filter:** ${author}\n`;
  md += `\n`;

  // Summary counts
  const severityCounts = {};
  const topicCounts = {};
  comments.forEach(c => {
    const s = c.severity || 'unclassified';
    const t = c.topic || 'unclassified';
    severityCounts[s] = (severityCounts[s] || 0) + 1;
    topicCounts[t] = (topicCounts[t] || 0) + 1;
  });

  md += `## Summary\n\n`;
  md += `### By Severity\n\n`;
  md += `| Severity | Count |\n|----------|-------|\n`;
  Object.entries(severityCounts).sort((a, b) => b[1] - a[1]).forEach(([s, count]) => {
    md += `| ${s.replace(/_/g, ' ')} | ${count} |\n`;
  });
  md += `\n### By Topic\n\n`;
  md += `| Topic | Count |\n|-------|-------|\n`;
  Object.entries(topicCounts).sort((a, b) => b[1] - a[1]).forEach(([t, count]) => {
    md += `| ${t.replace(/_/g, ' ')} | ${count} |\n`;
  });

  md += `\n---\n\n## Comments\n\n`;
  comments.forEach(c => {
    const s = c.severity || 'unclassified';
    const t = c.topic || 'unclassified';
    const date = formatDate(c.created_at);
    const prRef = c.pr_url ? c.pr_url.replace('https://github.com/', '') : '';
    md += `### ${c.author} — ${date}\n\n`;
    if (prRef) md += `**PR:** [${prRef}](${c.pr_url})\n`;
    md += `**Severity:** ${s.replace(/_/g, ' ')} | **Topic:** ${t.replace(/_/g, ' ')}`;
    if (c.confidence != null) md += ` | **Confidence:** ${(c.confidence * 100).toFixed(0)}%`;
    if (c.ai_classified) md += ` | _AI classified_`;
    md += `\n\n`;
    md += `${c.body}\n\n---\n\n`;
  });

  return md;
}

function downloadReport() {
  if (filteredComments.length === 0) return;
  const md = generateMarkdownReport(filteredComments);
  const blob = new Blob([md], { type: 'text/markdown' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  const from = document.getElementById('date-from').value;
  const to = document.getElementById('date-to').value;
  a.href = url;
  a.download = `review-comments-${from}-to-${to}.md`;
  a.click();
  URL.revokeObjectURL(url);
}

document.addEventListener('DOMContentLoaded', () => {
  initComponentFilter(renderReviews);
  initTimeRange(loadComments);

  document.getElementById('filter-severity').addEventListener('change', applyCommentFilters);
  document.getElementById('filter-topic').addEventListener('change', applyCommentFilters);
  document.getElementById('filter-author').addEventListener('change', applyCommentFilters);
  document.getElementById('filter-search').addEventListener('input', applyCommentFilters);
  document.getElementById('download-report').addEventListener('click', downloadReport);
});
