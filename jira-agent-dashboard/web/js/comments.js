// Comments classification summary page logic

let severityChart = null;
let topicChart = null;
let allComments = []; // stored for filtering
let filteredComments = []; // current filtered view

// Load comments data and render charts
async function loadComments(from, to) {
  try {
    allComments = await fetchAPI(`/api/comments/summary?from=${from}&to=${to}`);

    renderSeverityChart(allComments);
    renderTopicChart(allComments);
    renderPatternTable(allComments);
    populateAuthorFilter(allComments);
    applyCommentFilters();
  } catch (error) {
    showError('Failed to load comments data: ' + error.message);
  }
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

  const filtered = allComments.filter(c => {
    if (severity && (c.severity || 'unclassified') !== severity) return false;
    if (topic && (c.topic || 'unclassified') !== topic) return false;
    if (author && c.author !== author) return false;
    if (search && !(c.body || '').toLowerCase().includes(search) && !(c.author || '').toLowerCase().includes(search)) return false;
    return true;
  });

  filteredComments = filtered;
  document.getElementById('comment-count').textContent = `${filtered.length} of ${allComments.length} comments`;
  renderCommentList(filtered);
}

// Render severity breakdown chart
function renderSeverityChart(comments) {
  const ctx = document.getElementById('severity-chart').getContext('2d');

  if (severityChart) {
    severityChart.destroy();
  }

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

  if (topicChart) {
    topicChart.destroy();
  }

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

// Render pattern table (severity + topic combinations)
function renderPatternTable(comments) {
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
        count: 0
      };
    }
    patterns[key].count++;
  });

  const patternArray = Object.values(patterns).sort((a, b) => b.count - a.count);

  if (patternArray.length === 0) {
    tbody.innerHTML = '<tr><td colspan="3" style="text-align:center;">No comment patterns found.</td></tr>';
    return;
  }

  patternArray.forEach(pattern => {
    const row = document.createElement('tr');
    row.innerHTML = `
      <td><span class="tag ${pattern.severity}">${pattern.severity.replace(/_/g, ' ')}</span></td>
      <td><span class="tag ${pattern.topic}">${pattern.topic.replace(/_/g, ' ')}</span></td>
      <td>${formatNumber(pattern.count)}</td>
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

// --- Time Range Helpers (same as overview/issues) ---

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

  loadComments(from, to);
}

function applyCustomDateRange() {
  const from = document.getElementById('date-from').value;
  const to = document.getElementById('date-to').value;

  if (from && to) {
    document.querySelectorAll('.time-range button').forEach(btn => btn.classList.remove('active'));
    loadComments(from, to);
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

// Initialize on page load — default to last 7 days
document.addEventListener('DOMContentLoaded', () => {
  setupTimeRangeHandlers();

  document.getElementById('filter-severity').addEventListener('change', applyCommentFilters);
  document.getElementById('filter-topic').addEventListener('change', applyCommentFilters);
  document.getElementById('filter-author').addEventListener('change', applyCommentFilters);
  document.getElementById('filter-search').addEventListener('input', applyCommentFilters);
  document.getElementById('download-report').addEventListener('click', downloadReport);

  applyRange('7d');
});
