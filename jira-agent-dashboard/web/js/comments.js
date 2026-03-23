// Comments classification summary page logic

let severityChart = null;
let topicChart = null;

// Load comments data and render charts
async function loadComments(from, to) {
  try {
    // Fetch all issues in date range
    const issues = await fetchAPI(`/api/issues?from=${from}&to=${to}`);

    // Aggregate all comments from all issues
    const allComments = [];
    for (const issue of issues) {
      if (issue.id) {
        try {
          const comments = await fetchAPI(`/api/comments/${issue.id}`);
          if (comments && Array.isArray(comments)) {
            allComments.push(...comments);
          }
        } catch (error) {
          console.warn(`Failed to fetch comments for issue ${issue.id}:`, error);
        }
      }
    }

    // Render charts and table
    renderSeverityChart(allComments);
    renderTopicChart(allComments);
    renderPatternTable(allComments);
  } catch (error) {
    showError('Failed to load comments data: ' + error.message);
  }
}

// Render severity breakdown chart
function renderSeverityChart(comments) {
  const ctx = document.getElementById('severity-chart').getContext('2d');

  if (severityChart) {
    severityChart.destroy();
  }

  // Count comments by severity
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

  const labels = Object.keys(severityCounts).map(s => s.replace('_', ' ').toUpperCase());
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

  // Count comments by topic
  const topicCounts = {
    'style': 0,
    'logic_bug': 0,
    'test_gap': 0,
    'api_design': 0,
    'documentation': 0,
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

  const labels = Object.keys(topicCounts).map(t => t.replace('_', ' ').toUpperCase());
  const data = Object.values(topicCounts);
  const backgroundColors = [
    '#27ae60', // style - green
    '#e74c3c', // logic_bug - red
    '#f1c40f', // test_gap - yellow
    '#3498db', // api_design - blue
    '#2ecc71', // documentation - light green
    '#95a5a6'  // unclassified - gray
  ];

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

  // Count patterns (severity + topic combinations)
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

  // Convert to array and sort by count descending
  const patternArray = Object.values(patterns).sort((a, b) => b.count - a.count);

  if (patternArray.length === 0) {
    tbody.innerHTML = '<tr><td colspan="3" style="text-align:center;">No comment patterns found.</td></tr>';
    return;
  }

  // Render top patterns
  patternArray.forEach(pattern => {
    const row = document.createElement('tr');
    row.innerHTML = `
      <td><span class="tag ${pattern.severity}">${pattern.severity.replace('_', ' ')}</span></td>
      <td><span class="tag ${pattern.topic}">${pattern.topic.replace('_', ' ')}</span></td>
      <td>${formatNumber(pattern.count)}</td>
    `;
    tbody.appendChild(row);
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
  loadComments(fromStr, toStr);
}

function applyCustomDateRange() {
  const from = document.getElementById('date-from').value;
  const to = document.getElementById('date-to').value;

  if (from && to) {
    setDateRange(from, to);
    updateActiveButton(null);
    loadComments(from, to);
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
  loadComments(from, to);
});
