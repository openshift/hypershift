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

// Format duration in milliseconds to human-readable string
function formatDuration(ms) {
  if (ms == null || isNaN(ms) || ms < 0) return 'N/A';

  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (days > 0) {
    const remainingHours = hours % 24;
    return `${days}d ${remainingHours}h`;
  } else if (hours > 0) {
    const remainingMinutes = minutes % 60;
    return `${hours}h ${remainingMinutes}m`;
  } else if (minutes > 0) {
    const remainingSeconds = seconds % 60;
    return `${minutes}m ${remainingSeconds}s`;
  } else {
    return `${seconds}s`;
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
  const fromDate = new Date();
  fromDate.setDate(toDate.getDate() - 90);

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

// Display loading state
function showLoading(message = 'Loading') {
  return `<div class="loading">${message}</div>`;
}

// Initialize navigation highlighting
document.addEventListener('DOMContentLoaded', () => {
  highlightCurrentPage();
});
