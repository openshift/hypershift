// Trends page logic

let chartsInstances = {};

// Load trends data and render charts
async function loadTrends(from, to) {
  try {
    const data = await fetchAPI(`/api/trends?from=${from}&to=${to}`);

    // Update summary cards
    updateSummaryCards(data);

    // Render all charts
    renderMergeRateChart(data);
    renderReviewCommentsChart(data);
    renderCostChart(data);
    renderDurationChart(data);
    renderQualityScoreChart(data);
  } catch (error) {
    showError('Failed to load trends data: ' + error.message);
  }
}

// Update summary cards with aggregated metrics
function updateSummaryCards(data) {
  if (!data || data.length === 0) {
    document.getElementById('total-issues').textContent = '0';
    document.getElementById('merge-rate').textContent = 'N/A';
    document.getElementById('avg-cost').textContent = 'N/A';
    document.getElementById('avg-comments').textContent = 'N/A';
    return;
  }

  // Aggregate totals
  let totalIssues = 0;
  let totalMerged = 0;
  let totalCost = 0;
  let totalComments = 0;

  data.forEach(point => {
    totalIssues += point.total_issues || 0;
    totalMerged += point.merged_issues || 0;
    totalCost += point.avg_cost || 0;
    totalComments += point.avg_review_comments || 0;
  });

  const avgCost = data.length > 0 ? totalCost / data.length : 0;
  const avgComments = data.length > 0 ? totalComments / data.length : 0;
  const mergeRate = totalIssues > 0 ? (totalMerged / totalIssues * 100) : 0;

  document.getElementById('total-issues').textContent = formatNumber(totalIssues);
  document.getElementById('merge-rate').textContent = mergeRate.toFixed(1) + '%';
  document.getElementById('avg-cost').textContent = formatCost(avgCost);
  document.getElementById('avg-comments').textContent = avgComments.toFixed(1);
}

// Render merge rate trend chart
function renderMergeRateChart(data) {
  const ctx = document.getElementById('merge-rate-chart').getContext('2d');

  // Destroy existing chart if it exists
  if (chartsInstances.mergeRate) {
    chartsInstances.mergeRate.destroy();
  }

  const labels = data.map(d => d.week_start ? new Date(d.week_start).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) : '');
  const mergeRates = data.map(d => {
    const total = d.total_issues || 0;
    const merged = d.merged_issues || 0;
    return total > 0 ? (merged / total * 100) : 0;
  });

  chartsInstances.mergeRate = new Chart(ctx, {
    type: 'line',
    data: {
      labels: labels,
      datasets: [{
        label: 'Merge Rate (%)',
        data: mergeRates,
        borderColor: '#27ae60',
        backgroundColor: 'rgba(39, 174, 96, 0.1)',
        tension: 0.4,
        fill: true
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          display: false
        }
      },
      scales: {
        y: {
          beginAtZero: true,
          max: 100,
          ticks: {
            callback: function(value) {
              return value + '%';
            }
          }
        }
      }
    }
  });
}

// Render review comments by severity chart
function renderReviewCommentsChart(data) {
  const ctx = document.getElementById('review-comments-chart').getContext('2d');

  if (chartsInstances.reviewComments) {
    chartsInstances.reviewComments.destroy();
  }

  const labels = data.map(d => d.week_start ? new Date(d.week_start).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) : '');
  const avgComments = data.map(d => d.avg_review_comments || 0);

  chartsInstances.reviewComments = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [{
        label: 'Avg Review Comments',
        data: avgComments,
        backgroundColor: '#3498db',
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          display: false
        }
      },
      scales: {
        y: {
          beginAtZero: true
        }
      }
    }
  });
}

// Render cost trend chart
function renderCostChart(data) {
  const ctx = document.getElementById('cost-chart').getContext('2d');

  if (chartsInstances.cost) {
    chartsInstances.cost.destroy();
  }

  const labels = data.map(d => d.week_start ? new Date(d.week_start).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) : '');
  const costs = data.map(d => d.avg_cost || 0);

  chartsInstances.cost = new Chart(ctx, {
    type: 'line',
    data: {
      labels: labels,
      datasets: [{
        label: 'Avg Cost ($)',
        data: costs,
        borderColor: '#f39c12',
        backgroundColor: 'rgba(243, 156, 18, 0.1)',
        tension: 0.4,
        fill: true
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          display: false
        }
      },
      scales: {
        y: {
          beginAtZero: true,
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

// Render duration trend chart
function renderDurationChart(data) {
  const ctx = document.getElementById('duration-chart').getContext('2d');

  if (chartsInstances.duration) {
    chartsInstances.duration.destroy();
  }

  const labels = data.map(d => d.week_start ? new Date(d.week_start).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) : '');
  const durations = data.map(d => {
    // Convert milliseconds to hours for easier reading
    const ms = d.avg_merge_duration || 0;
    return ms / (1000 * 60 * 60);
  });

  chartsInstances.duration = new Chart(ctx, {
    type: 'line',
    data: {
      labels: labels,
      datasets: [{
        label: 'Avg Merge Duration (hours)',
        data: durations,
        borderColor: '#9b59b6',
        backgroundColor: 'rgba(155, 89, 182, 0.1)',
        tension: 0.4,
        fill: true
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          display: false
        }
      },
      scales: {
        y: {
          beginAtZero: true,
          ticks: {
            callback: function(value) {
              return value.toFixed(1) + 'h';
            }
          }
        }
      }
    }
  });
}

// Render quality score trend chart
function renderQualityScoreChart(data) {
  const ctx = document.getElementById('quality-score-chart').getContext('2d');

  if (chartsInstances.qualityScore) {
    chartsInstances.qualityScore.destroy();
  }

  const labels = data.map(d => d.week_start ? new Date(d.week_start).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) : '');
  const scores = data.map(d => d.avg_quality_score || 0);

  chartsInstances.qualityScore = new Chart(ctx, {
    type: 'line',
    data: {
      labels: labels,
      datasets: [{
        label: 'Avg Quality Score',
        data: scores,
        borderColor: '#e74c3c',
        backgroundColor: 'rgba(231, 76, 60, 0.1)',
        tension: 0.4,
        fill: true
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          display: false
        }
      },
      scales: {
        y: {
          beginAtZero: true,
          max: 100
        }
      }
    }
  });
}

// Time range button handlers
function setupTimeRangeHandlers() {
  // Preset buttons
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

  // Custom date inputs
  document.getElementById('date-from').addEventListener('change', applyCustomDateRange);
  document.getElementById('date-to').addEventListener('change', applyCustomDateRange);
}

function applyDateRange(from, to) {
  const fromStr = from.toISOString().split('T')[0];
  const toStr = to.toISOString().split('T')[0];

  // Update URL
  setDateRange(fromStr, toStr);

  // Update input fields
  document.getElementById('date-from').value = fromStr;
  document.getElementById('date-to').value = toStr;

  // Update active button
  updateActiveButton(null);

  // Reload data
  loadTrends(fromStr, toStr);
}

function applyCustomDateRange() {
  const from = document.getElementById('date-from').value;
  const to = document.getElementById('date-to').value;

  if (from && to) {
    setDateRange(from, to);
    updateActiveButton(null);
    loadTrends(from, to);
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

  // Set initial date input values
  document.getElementById('date-from').value = from;
  document.getElementById('date-to').value = to;

  // Determine which button should be active
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

  // Setup event handlers
  setupTimeRangeHandlers();

  // Load initial data
  loadTrends(from, to);
});
