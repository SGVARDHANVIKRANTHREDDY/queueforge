/**
 * QueueForge Dashboard - Pure Vanilla JavaScript SPA
 * Memory-only key session (never stored in localStorage)
 */

// In-memory session state
let sessionState = {
  baseUrl: window.location.origin,
  apiKey: "",
  activeTab: "overview",
  pollIntervalId: null,
};

// UI Element Selectors
const els = {
  sidebarTenantName: document.getElementById("sidebar-tenant-name"),
  viewTitle: document.getElementById("view-title"),
  statQueued: document.getElementById("stat-queued"),
  statRunning: document.getElementById("stat-running"),
  statSucceeded: document.getElementById("stat-succeeded"),
  statDead: document.getElementById("stat-dead"),
  badgeQueuedCount: document.getElementById("badge-queued-count"),
  badgeDeadCount: document.getElementById("badge-dead-count"),

  tbodyQueues: document.getElementById("tbody-queues"),
  workerListCards: document.getElementById("worker-list-cards"),
  tbodyRecentJobs: document.getElementById("tbody-recent-jobs"),
  tbodyJobsFull: document.getElementById("tbody-jobs-full"),
  tbodySchedules: document.getElementById("tbody-schedules"),
  tbodyDeadletters: document.getElementById("tbody-deadletters"),
  tbodyWorkersFull: document.getElementById("tbody-workers-full"),

  modalAuth: document.getElementById("modal-auth"),
  formAuth: document.getElementById("form-auth"),
  authUrl: document.getElementById("auth-url"),
  authKey: document.getElementById("auth-key"),
  btnAuthModal: document.getElementById("btn-auth-modal"),
  btnCloseAuthModal: document.getElementById("btn-close-auth-modal"),

  modalEnqueue: document.getElementById("modal-enqueue"),
  formEnqueue: document.getElementById("form-enqueue"),
  btnEnqueueModal: document.getElementById("btn-enqueue-modal"),
  btnCloseEnqueueModal: document.getElementById("btn-close-enqueue-modal"),

  modalSchedule: document.getElementById("modal-schedule"),
  formSchedule: document.getElementById("form-schedule"),
  btnCreateScheduleModal: document.getElementById("btn-create-schedule-modal"),
  btnCloseSchedModal: document.getElementById("btn-close-sched-modal"),

  modalJobDetail: document.getElementById("modal-job-detail"),
  btnCloseJobModal: document.getElementById("btn-close-job-modal"),
  modalJobId: document.getElementById("modal-job-id"),
  detailStatus: document.getElementById("detail-status"),
  detailQueue: document.getElementById("detail-queue"),
  detailTask: document.getElementById("detail-task"),
  detailPriority: document.getElementById("detail-priority"),
  detailCreated: document.getElementById("detail-created"),
  detailAttempts: document.getElementById("detail-attempts"),
  detailPayload: document.getElementById("detail-payload"),
  detailResult: document.getElementById("detail-result"),
  tbodyAttempts: document.getElementById("tbody-attempts"),
  btnDetailCancel: document.getElementById("btn-detail-cancel"),
  btnDetailReplay: document.getElementById("btn-detail-replay"),

  btnRefresh: document.getElementById("btn-refresh"),
  btnApplyFilters: document.getElementById("btn-apply-filters"),
  btnResetFilters: document.getElementById("btn-reset-filters"),
  filterStatus: document.getElementById("filter-status"),
  filterQueue: document.getElementById("filter-queue"),
  filterTaskType: document.getElementById("filter-task-type"),
};

// Initialize
document.addEventListener("DOMContentLoaded", () => {
  setupNavigation();
  setupModals();
  setupForms();

  // Prompt auth if not set
  if (!sessionState.apiKey) {
    openModal(els.modalAuth);
  } else {
    startLivePolling();
  }
});

// HTTP Request Helper
async function apiRequest(path, method = "GET", body = null, extraHeaders = {}) {
  const url = `${sessionState.baseUrl}${path}`;
  const headers = {
    "Accept": "application/json",
    ...extraHeaders,
  };
  if (sessionState.apiKey) {
    headers["Authorization"] = `Bearer ${sessionState.apiKey}`;
  }
  if (body) {
    headers["Content-Type"] = "application/json";
  }

  const res = await fetch(url, {
    method,
    headers,
    body: body ? JSON.stringify(body) : null,
  });

  if (!res.ok) {
    let errCode = "error";
    let errMsg = `Request failed: ${res.statusText}`;
    try {
      const errJson = await res.json();
      if (errJson.error) {
        errCode = errJson.error.code || errCode;
        errMsg = errJson.error.message || errMsg;
      }
    } catch (_) {}
    throw new Error(`[${errCode}] ${errMsg}`);
  }

  if (res.status === 204) return null;
  return await res.json();
}

// Navigation & Tab Switching
function setupNavigation() {
  document.querySelectorAll(".nav-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const tab = btn.getAttribute("data-tab");
      switchTab(tab);
    });
  });

  const btnViewAll = document.getElementById("btn-view-all-jobs");
  if (btnViewAll) {
    btnViewAll.addEventListener("click", () => switchTab("jobs"));
  }

  els.btnRefresh.addEventListener("click", () => fetchActiveTabData());
}

function switchTab(tab) {
  sessionState.activeTab = tab;
  document.querySelectorAll(".nav-btn").forEach((b) => b.classList.remove("active"));
  const activeBtn = document.querySelector(`.nav-btn[data-tab="${tab}"]`);
  if (activeBtn) activeBtn.classList.add("active");

  document.querySelectorAll(".view-panel").forEach((p) => p.classList.remove("active"));
  const activePanel = document.getElementById(`view-${tab}`);
  if (activePanel) activePanel.classList.add("active");

  const titles = {
    overview: "System Overview",
    jobs: "Job Submissions & Lifecycle",
    schedules: "Recurring Cron Schedules",
    deadletters: "Dead-Letter Queue & Replay",
    workers: "Registered Worker Instances",
  };
  els.viewTitle.textContent = titles[tab] || "Dashboard";

  fetchActiveTabData();
}

// Polling and Data Refresh
function startLivePolling() {
  if (sessionState.pollIntervalId) clearInterval(sessionState.pollIntervalId);
  fetchActiveTabData();
  sessionState.pollIntervalId = setInterval(() => {
    fetchActiveTabData(true);
  }, 3000);
}

async function fetchActiveTabData(isBackground = false) {
  if (!sessionState.apiKey) return;

  try {
    // Overview metrics always fetched
    await fetchOverviewStats();

    if (sessionState.activeTab === "overview") {
      await Promise.all([fetchQueueBreakdown(), fetchWorkersSummary(), fetchRecentJobs()]);
    } else if (sessionState.activeTab === "jobs") {
      await fetchJobsList();
    } else if (sessionState.activeTab === "schedules") {
      await fetchSchedules();
    } else if (sessionState.activeTab === "deadletters") {
      await fetchDeadLetters();
    } else if (sessionState.activeTab === "workers") {
      await fetchWorkersFull();
    }
  } catch (err) {
    if (!isBackground) {
      console.warn("Poll fetch notice:", err.message);
    }
  }
}

// Fetch Overview Stats
async function fetchOverviewStats() {
  const queues = ["default", "notifications", "background", "maintenance"];
  let totalQueued = 0;
  let totalRunning = 0;
  let totalSucceeded = 0;
  let totalDead = 0;

  for (const q of queues) {
    try {
      const data = await apiRequest(`/v1/queues/${q}/stats`);
      const s = data.stats || {};
      totalQueued += s.queued || 0;
      totalRunning += s.running || 0;
      totalSucceeded += s.succeeded || 0;
      totalDead += s.dead || 0;
    } catch (_) {}
  }

  els.statQueued.textContent = totalQueued;
  els.statRunning.textContent = totalRunning;
  els.statSucceeded.textContent = totalSucceeded;
  els.statDead.textContent = totalDead;
  els.badgeQueuedCount.textContent = totalQueued;
  els.badgeDeadCount.textContent = totalDead;
}

// Fetch Queue Breakdown
async function fetchQueueBreakdown() {
  const queues = ["default", "notifications", "background", "maintenance"];
  let rowsHtml = "";

  for (const q of queues) {
    try {
      const data = await apiRequest(`/v1/queues/${q}/stats`);
      const s = data.stats || {};
      rowsHtml += `
        <tr>
          <td><strong>${q}</strong></td>
          <td><span class="status-pill queued">${s.queued || 0}</span></td>
          <td><span class="status-pill running">${s.running || 0}</span></td>
          <td><span class="status-pill succeeded">${s.succeeded || 0}</span></td>
          <td><span class="status-pill dead">${s.dead || 0}</span></td>
        </tr>
      `;
    } catch (_) {
      rowsHtml += `<tr><td><strong>${q}</strong></td><td colspan="4" class="empty-cell">Offline</td></tr>`;
    }
  }
  els.tbodyQueues.innerHTML = rowsHtml;
}

// Fetch Workers
async function fetchWorkersSummary() {
  try {
    const data = await apiRequest("/v1/workers");
    const workers = data.workers || [];
    if (workers.length === 0) {
      els.workerListCards.innerHTML = '<div class="empty-state">No workers registered recently.</div>';
      return;
    }

    els.workerListCards.innerHTML = workers.map(w => `
      <div class="worker-card">
        <div class="worker-header">
          <span class="status-dot online"></span>
          <strong>${escapeHtml(w.hostname || w.process_name)}</strong>
          <span class="tag">${escapeHtml(w.version)}</span>
        </div>
        <div class="worker-body">
          <small>Queues: ${w.queues.join(", ")}</small><br>
          <small>Tasks: ${w.task_types.slice(0, 3).join(", ")}${w.task_types.length > 3 ? "..." : ""}</small>
        </div>
      </div>
    `).join("");
  } catch (_) {}
}

async function fetchWorkersFull() {
  try {
    const data = await apiRequest("/v1/workers");
    const workers = data.workers || [];
    if (workers.length === 0) {
      els.tbodyWorkersFull.innerHTML = '<tr><td colspan="7" class="empty-cell">No active worker instances found.</td></tr>';
      return;
    }

    els.tbodyWorkersFull.innerHTML = workers.map(w => `
      <tr>
        <td><code>${w.id.substring(0, 8)}...</code></td>
        <td>${escapeHtml(w.process_name)}</td>
        <td>${escapeHtml(w.hostname || "unknown")}</td>
        <td>${w.queues.map(q => `<span class="tag">${q}</span>`).join(" ")}</td>
        <td>${w.task_types.map(t => `<code>${t}</code>`).join(", ")}</td>
        <td>${formatTime(w.last_seen_at)}</td>
        <td><span class="status-pill succeeded">Online</span></td>
      </tr>
    `).join("");
  } catch (_) {}
}

// Fetch Recent Jobs
async function fetchRecentJobs() {
  try {
    const data = await apiRequest("/v1/jobs?limit=8");
    const jobs = data.jobs || [];
    if (jobs.length === 0) {
      els.tbodyRecentJobs.innerHTML = '<tr><td colspan="7" class="empty-cell">No jobs submitted yet.</td></tr>';
      return;
    }

    els.tbodyRecentJobs.innerHTML = jobs.map(j => renderJobRow(j, false)).join("");
  } catch (_) {}
}

// Fetch Full Jobs List with Filters
async function fetchJobsList() {
  const status = els.filterStatus.value;
  const queue = els.filterQueue.value;
  const taskType = els.filterTaskType.value.trim();

  let params = ["limit=50"];
  if (status) params.push(`status=${encodeURIComponent(status)}`);
  if (queue) params.push(`queue=${encodeURIComponent(queue)}`);
  if (taskType) params.push(`task_type=${encodeURIComponent(taskType)}`);

  try {
    const data = await apiRequest(`/v1/jobs?${params.join("&")}`);
    const jobs = data.jobs || [];
    if (jobs.length === 0) {
      els.tbodyJobsFull.innerHTML = '<tr><td colspan="8" class="empty-cell">No jobs match filters.</td></tr>';
      return;
    }

    els.tbodyJobsFull.innerHTML = jobs.map(j => renderJobRow(j, true)).join("");
  } catch (err) {
    els.tbodyJobsFull.innerHTML = `<tr><td colspan="8" class="empty-cell">Error: ${escapeHtml(err.message)}</td></tr>`;
  }
}

function renderJobRow(j, showAttempts = false) {
  const statusClass = j.status;
  return `
    <tr>
      <td><a href="#" onclick="openJobDetail('${j.id}'); return false;"><code>${j.id.substring(0, 8)}...</code></a></td>
      <td><strong>${escapeHtml(j.task_type)}</strong></td>
      <td><span class="tag">${escapeHtml(j.queue_name)}</span></td>
      <td>${j.priority}</td>
      ${showAttempts ? `<td>${j.attempts_started} / ${j.max_attempts}</td>` : ""}
      <td><span class="status-pill ${statusClass}">${j.status}</span></td>
      <td>${formatTime(j.created_at)}</td>
      <td>
        <button class="btn btn-sm btn-secondary" onclick="openJobDetail('${j.id}')">View</button>
      </td>
    </tr>
  `;
}

// Open Job Details
window.openJobDetail = async function(jobID) {
  try {
    const job = await apiRequest(`/v1/jobs/${jobID}`);
    els.modalJobId.textContent = `Job: ${job.id}`;
    els.detailStatus.textContent = job.status;
    els.detailStatus.className = `status-pill ${job.status}`;
    els.detailQueue.textContent = job.queue;
    els.detailTask.textContent = job.task_type;
    els.detailPriority.textContent = job.priority;
    els.detailCreated.textContent = formatTime(job.created_at);
    els.detailAttempts.textContent = `${job.attempts_started} / ${job.max_attempts}`;

    els.detailPayload.textContent = JSON.stringify(job.payload, null, 2);
    els.detailResult.textContent = job.result ? JSON.stringify(job.result, null, 2) : "None yet";

    // Attempts table
    const attempts = job.attempts || [];
    if (attempts.length === 0) {
      els.tbodyAttempts.innerHTML = '<tr><td colspan="6" class="empty-cell">No attempts recorded yet</td></tr>';
    } else {
      els.tbodyAttempts.innerHTML = attempts.map(a => `
        <tr>
          <td>${a.attempt_number}</td>
          <td><span class="status-pill ${a.outcome || 'running'}">${a.outcome || 'running'}</span></td>
          <td>${a.duration_ms !== null ? a.duration_ms + 'ms' : '--'}</td>
          <td>${escapeHtml(a.error_code || '--')}</td>
          <td><small>${escapeHtml(a.error_message || '--')}</small></td>
          <td>${formatTime(a.started_at)}</td>
        </tr>
      `).join("");
    }

    // Cancel / Replay action buttons
    els.btnDetailCancel.onclick = async () => {
      if (confirm("Are you sure you want to cancel this job?")) {
        await apiRequest(`/v1/jobs/${job.id}/cancel`, "POST");
        alert("Cancellation requested.");
        closeModal(els.modalJobDetail);
        fetchActiveTabData();
      }
    };

    if (job.status === "dead") {
      els.btnDetailReplay.style.display = "inline-flex";
      els.btnDetailReplay.onclick = async () => {
        if (confirm("Replay this dead job? A new execution attempt will be scheduled.")) {
          await apiRequest(`/v1/jobs/${job.id}/replay`, "POST", { reason: "Operator dashboard replay" });
          alert("Job replayed to queued state.");
          closeModal(els.modalJobDetail);
          fetchActiveTabData();
        }
      };
    } else {
      els.btnDetailReplay.style.display = "none";
    }

    openModal(els.modalJobDetail);
  } catch (err) {
    alert(`Failed to load job details: ${err.message}`);
  }
};

// Fetch Schedules
async function fetchSchedules() {
  try {
    const data = await apiRequest("/v1/schedules");
    const schedules = data.schedules || [];
    if (schedules.length === 0) {
      els.tbodySchedules.innerHTML = '<tr><td colspan="9" class="empty-cell">No recurring schedules.</td></tr>';
      return;
    }

    els.tbodySchedules.innerHTML = schedules.map(s => `
      <tr>
        <td><strong>${escapeHtml(s.name)}</strong></td>
        <td><code>${escapeHtml(s.task_type)}</code></td>
        <td><span class="tag">${escapeHtml(s.queue_name)}</span></td>
        <td><code>${escapeHtml(s.cron_expression)}</code></td>
        <td>${escapeHtml(s.timezone)}</td>
        <td>${escapeHtml(s.misfire_policy)}</td>
        <td>${formatTime(s.next_run_at)}</td>
        <td>
          <span class="status-pill ${s.paused ? 'dead' : 'succeeded'}">${s.paused ? 'Paused' : 'Active'}</span>
        </td>
        <td>
          <button class="btn btn-sm btn-secondary" onclick="toggleSchedulePause('${s.id}', ${!s.paused})">
            ${s.paused ? 'Resume' : 'Pause'}
          </button>
          <button class="btn btn-sm btn-danger" onclick="deleteSchedule('${s.id}')">Disable</button>
        </td>
      </tr>
    `).join("");
  } catch (_) {}
}

window.toggleSchedulePause = async function(id, paused) {
  try {
    await apiRequest(`/v1/schedules/${id}`, "PATCH", { paused });
    fetchSchedules();
  } catch (err) {
    alert(`Failed to toggle schedule: ${err.message}`);
  }
};

window.deleteSchedule = async function(id) {
  if (confirm("Disable this recurring schedule? Existing generated jobs will remain independent.")) {
    try {
      await apiRequest(`/v1/schedules/${id}`, "DELETE");
      fetchSchedules();
    } catch (err) {
      alert(`Failed to disable schedule: ${err.message}`);
    }
  }
};

// Fetch Dead Letters
async function fetchDeadLetters() {
  try {
    const data = await apiRequest("/v1/dead-letters");
    const jobs = data.dead_letters || [];
    if (jobs.length === 0) {
      els.tbodyDeadletters.innerHTML = '<tr><td colspan="8" class="empty-cell">No dead-letter jobs. System healthy!</td></tr>';
      return;
    }

    els.tbodyDeadletters.innerHTML = jobs.map(j => `
      <tr>
        <td><a href="#" onclick="openJobDetail('${j.id}'); return false;"><code>${j.id.substring(0, 8)}...</code></a></td>
        <td><strong>${escapeHtml(j.task_type)}</strong></td>
        <td><span class="tag">${escapeHtml(j.queue_name)}</span></td>
        <td>${j.attempts_started} / ${j.max_attempts}</td>
        <td><span class="tag" style="color:#ef4444">${escapeHtml(j.last_error_code || 'unknown')}</span></td>
        <td><small>${escapeHtml(j.last_error_message || '--')}</small></td>
        <td>${formatTime(j.completed_at || j.updated_at)}</td>
        <td>
          <button class="btn btn-sm btn-primary" onclick="quickReplay('${j.id}')">Replay</button>
        </td>
      </tr>
    `).join("");
  } catch (_) {}
}

window.quickReplay = async function(jobID) {
  if (confirm(`Confirm replay for job ${jobID}?`)) {
    try {
      await apiRequest(`/v1/jobs/${jobID}/replay`, "POST", { reason: "Quick replay from Dead-Letter tab" });
      alert("Job replayed successfully!");
      fetchActiveTabData();
    } catch (err) {
      alert(`Replay failed: ${err.message}`);
    }
  }
};

// Modal helpers
function openModal(modal) {
  modal.classList.add("active");
}
function closeModal(modal) {
  modal.classList.remove("active");
}

function setupModals() {
  els.btnAuthModal.addEventListener("click", () => openModal(els.modalAuth));
  els.btnCloseAuthModal.addEventListener("click", () => closeModal(els.modalAuth));

  els.btnEnqueueModal.addEventListener("click", () => openModal(els.modalEnqueue));
  els.btnCloseEnqueueModal.addEventListener("click", () => closeModal(els.modalEnqueue));

  els.btnCreateScheduleModal.addEventListener("click", () => openModal(els.modalSchedule));
  els.btnCloseSchedModal.addEventListener("click", () => closeModal(els.modalSchedule));

  els.btnCloseJobModal.addEventListener("click", () => closeModal(els.modalJobDetail));

  els.btnApplyFilters.addEventListener("click", () => fetchJobsList());
  els.btnResetFilters.addEventListener("click", () => {
    els.filterStatus.value = "";
    els.filterQueue.value = "";
    els.filterTaskType.value = "";
    fetchJobsList();
  });
}

function setupForms() {
  // Auth Form
  els.formAuth.addEventListener("submit", async (e) => {
    e.preventDefault();
    const url = els.authUrl.value.trim().replace(/\/$/, "");
    const key = els.authKey.value.trim();
    if (!key) {
      alert("Please enter a valid API key.");
      return;
    }

    sessionState.baseUrl = url;
    sessionState.apiKey = key;

    try {
      const data = await apiRequest("/v1/tenant/limits");
      els.sidebarTenantName.textContent = `Tenant: ${data.tenant_id.substring(0, 8)}...`;
      closeModal(els.modalAuth);
      startLivePolling();
    } catch (err) {
      alert(`Connection failed: ${err.message}`);
    }
  });

  // Enqueue Form
  els.formEnqueue.addEventListener("submit", async (e) => {
    e.preventDefault();
    const taskType = document.getElementById("eq-task").value.trim();
    const queue = document.getElementById("eq-queue").value;
    const priority = parseInt(document.getElementById("eq-priority").value, 10);
    const maxAttempts = parseInt(document.getElementById("eq-max-attempts").value, 10);
    const idempotencyKey = document.getElementById("eq-idempotency").value.trim();
    const payloadRaw = document.getElementById("eq-payload").value.trim();

    let payload = {};
    try {
      payload = JSON.parse(payloadRaw);
    } catch (err) {
      alert("Invalid JSON in payload: " + err.message);
      return;
    }

    try {
      const headers = {};
      if (idempotencyKey) {
        headers["Idempotency-Key"] = idempotencyKey;
      }
      const res = await apiRequest("/v1/jobs", "POST", {
        task_type: taskType,
        queue,
        priority,
        max_attempts: maxAttempts,
        payload,
      }, headers);

      alert(`Job enqueued successfully! ID: ${res.id}`);
      closeModal(els.modalEnqueue);
      fetchActiveTabData();
    } catch (err) {
      alert(`Enqueue error: ${err.message}`);
    }
  });

  // Create Schedule Form
  els.formSchedule.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = document.getElementById("sc-name").value.trim();
    const taskType = document.getElementById("sc-task").value.trim();
    const cronExpression = document.getElementById("sc-cron").value.trim();
    const timezone = document.getElementById("sc-tz").value.trim();
    const misfirePolicy = document.getElementById("sc-misfire").value;
    const payloadRaw = document.getElementById("sc-payload").value.trim();

    let payload = {};
    try {
      if (payloadRaw) payload = JSON.parse(payloadRaw);
    } catch (err) {
      alert("Invalid JSON in payload: " + err.message);
      return;
    }

    try {
      await apiRequest("/v1/schedules", "POST", {
        name,
        task_type: taskType,
        cron_expression: cronExpression,
        timezone,
        misfire_policy: misfirePolicy,
        payload,
      });

      alert("Schedule created successfully!");
      closeModal(els.modalSchedule);
      fetchSchedules();
    } catch (err) {
      alert(`Schedule error: ${err.message}`);
    }
  });
}

function escapeHtml(str) {
  if (!str) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

function formatTime(isoStr) {
  if (!isoStr) return "--";
  try {
    const d = new Date(isoStr);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) + " " + d.toLocaleDateString();
  } catch (_) {
    return isoStr;
  }
}
