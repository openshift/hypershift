// Issue detail page logic

let issueData = null;
let phaseChart = null;

// Load issue detail from API
async function loadIssueDetail(id) {
  try {
    issueData = await fetchAPI(`/api/issues/${id}`);
    renderIssueDetail();
  } catch (error) {
    showError('Failed to load issue details: ' + error.message);
  }
}

// Render issue detail page
function renderIssueDetail() {
  if (!issueData) return;

  // Update page title and breadcrumb
  document.title = `${issueData.jira_key} - JIRA Agent Dashboard`;
  document.getElementById('breadcrumb-key').textContent = issueData.jira_key;

  // Update header
  const header = document.getElementById('issue-header');
  const statusClass = issueData.pr_merged ? 'merged' : (issueData.pr_closed ? 'closed' : 'open');
  const status = issueData.pr_merged ? 'merged' : (issueData.pr_closed ? 'closed' : 'open');

  header.innerHTML = `
    <h2>
      <a href="${issueData.jira_url}" target="_blank">${issueData.jira_key}</a>
    </h2>
    <div class="meta">
      <span><a href="${issueData.pr_url}" target="_blank">PR #${issueData.pr_number}</a></span>
      <span><span class="badge ${statusClass}">${status}</span></span>
      <span>Duration: ${formatDuration(issueData.merge_duration)}</span>
      <span>Cost: ${formatCost(issueData.total_cost)}</span>
    </div>
  `;

  // Render phase breakdown
  renderPhaseBreakdown();

  // Render PR metrics
  renderPRMetrics();

  // Render review comments
  renderReviewComments();
}

// Render phase breakdown chart
function renderPhaseBreakdown() {
  const ctx = document.getElementById('phase-chart').getContext('2d');

  if (phaseChart) {
    phaseChart.destroy();
  }

  if (!issueData.phases || issueData.phases.length === 0) {
    document.getElementById('phase-breakdown').innerHTML = '<p>No phase data available.</p>';
    return;
  }

  const labels = issueData.phases.map(p => p.phase_name);
  const durations = issueData.phases.map(p => {
    // Convert milliseconds to minutes for better readability
    const ms = p.duration || 0;
    return ms / (1000 * 60);
  });
  const costs = issueData.phases.map(p => p.cost || 0);

  phaseChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [
        {
          label: 'Duration (minutes)',
          data: durations,
          backgroundColor: '#3498db',
          yAxisID: 'y'
        },
        {
          label: 'Cost ($)',
          data: costs,
          backgroundColor: '#f39c12',
          yAxisID: 'y1'
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      indexAxis: 'y',
      scales: {
        y: {
          beginAtZero: true,
          ticks: {
            callback: function(value) {
              return value.toFixed(0) + 'm';
            }
          }
        },
        y1: {
          beginAtZero: true,
          position: 'right',
          grid: {
            drawOnChartArea: false
          },
          ticks: {
            callback: function(value) {
              return '$' + value.toFixed(2);
            }
          }
        }
      }
    }
  });
}

// Render PR metrics cards
function renderPRMetrics() {
  const container = document.getElementById('pr-metrics');
  container.innerHTML = `
    <div class="metric-card">
      <div class="label">Lines Added</div>
      <div class="value">${formatNumber(issueData.lines_added || 0)}</div>
    </div>
    <div class="metric-card">
      <div class="label">Lines Deleted</div>
      <div class="value">${formatNumber(issueData.lines_deleted || 0)}</div>
    </div>
    <div class="metric-card">
      <div class="label">Files Changed</div>
      <div class="value">${formatNumber(issueData.files_changed || 0)}</div>
    </div>
    <div class="metric-card">
      <div class="label">Complexity Delta</div>
      <div class="value">${formatNumber(issueData.complexity_delta || 0)}</div>
    </div>
    <div class="metric-card">
      <div class="label">Quality Score</div>
      <div class="value">${issueData.quality_score != null ? issueData.quality_score.toFixed(1) : 'N/A'}</div>
    </div>
    <div class="metric-card">
      <div class="label">Review Comments</div>
      <div class="value">${formatNumber(issueData.review_comment_count || 0)}</div>
    </div>
  `;
}

// Render review comments list
function renderReviewComments() {
  const container = document.getElementById('comments-list');

  if (!issueData.comments || issueData.comments.length === 0) {
    container.innerHTML = '<p>No review comments found.</p>';
    return;
  }

  container.innerHTML = '';

  issueData.comments.forEach(comment => {
    const commentDiv = document.createElement('div');
    commentDiv.className = 'comment';
    commentDiv.dataset.commentId = comment.id;

    const severityOptions = ['', 'nitpick', 'suggestion', 'required_change', 'question'];
    const topicOptions = ['', 'style', 'logic_bug', 'test_gap', 'api_design', 'documentation'];

    const severitySelect = severityOptions.map(opt =>
      `<option value="${opt}" ${comment.severity === opt ? 'selected' : ''}>${opt || 'Not classified'}</option>`
    ).join('');

    const topicSelect = topicOptions.map(opt =>
      `<option value="${opt}" ${comment.topic === opt ? 'selected' : ''}>${opt || 'Not classified'}</option>`
    ).join('');

    commentDiv.innerHTML = `
      <div class="comment-header">
        <span class="comment-author">${comment.author || 'Unknown'}</span>
        <small>${formatDate(comment.created_at)}</small>
      </div>
      <div class="comment-body">${escapeHtml(comment.body || '')}</div>
      <div class="comment-classification">
        <label>Severity:</label>
        <select class="severity-select" data-comment-id="${comment.id}">
          ${severitySelect}
        </select>
        <label>Topic:</label>
        <select class="topic-select" data-comment-id="${comment.id}">
          ${topicSelect}
        </select>
        <button class="save-btn" data-comment-id="${comment.id}">Save</button>
      </div>
    `;

    container.appendChild(commentDiv);
  });

  // Attach save handlers
  document.querySelectorAll('.save-btn').forEach(btn => {
    btn.addEventListener('click', handleSaveClassification);
  });
}

// Handle save classification
async function handleSaveClassification(event) {
  const button = event.target;
  const commentId = button.dataset.commentId;
  const severitySelect = document.querySelector(`.severity-select[data-comment-id="${commentId}"]`);
  const topicSelect = document.querySelector(`.topic-select[data-comment-id="${commentId}"]`);

  const severity = severitySelect.value;
  const topic = topicSelect.value;

  button.disabled = true;
  button.textContent = 'Saving...';

  try {
    const response = await fetch(`/api/comments/${commentId}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ severity, topic })
    });

    if (!response.ok) {
      throw new Error(`Failed to save: ${response.statusText}`);
    }

    button.textContent = 'Saved!';
    setTimeout(() => {
      button.textContent = 'Save';
      button.disabled = false;
    }, 2000);

    // Update local data
    const comment = issueData.comments.find(c => c.id === parseInt(commentId));
    if (comment) {
      comment.severity = severity;
      comment.topic = topic;
    }
  } catch (error) {
    alert('Failed to save classification: ' + error.message);
    button.textContent = 'Save';
    button.disabled = false;
  }
}

// Escape HTML to prevent XSS
function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

// Get issue ID from URL
function getIssueIdFromURL() {
  const params = new URLSearchParams(window.location.search);
  return params.get('id');
}

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
  const issueId = getIssueIdFromURL();
  if (!issueId) {
    showError('No issue ID provided in URL');
    return;
  }
  loadIssueDetail(issueId);
});
