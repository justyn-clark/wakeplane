const state = {
	token: localStorage.getItem("wakeplane.authToken") || "",
	status: null,
	health: null,
	ready: null,
	schedules: [],
	runs: [],
	nextRunCursor: null,
	view: "ledger",
};

const el = (id) => document.getElementById(id);
const fmt = new Intl.DateTimeFormat(undefined, {
	month: "short",
	day: "2-digit",
	hour: "2-digit",
	minute: "2-digit",
	second: "2-digit",
});

function authHeaders() {
	return state.token ? { Authorization: `Bearer ${state.token}` } : {};
}

async function api(path, options = {}) {
	const headers = { ...(options.headers || {}), ...authHeaders() };
	if (options.body) headers["Content-Type"] = "application/json";
	const res = await fetch(path, { ...options, headers });
	if (res.status === 401) {
		throw new Error("Auth required. Set the operator token.");
	}
	const text = await res.text();
	const body = text ? JSON.parse(text) : null;
	if (!res.ok) {
		throw new Error(body?.error || `${res.status} ${res.statusText}`);
	}
	return body;
}

function shortID(id) {
	if (!id) return "";
	const parts = id.split("_");
	const tail = parts[parts.length - 1] || id;
	return `${parts[0]}_${tail.slice(0, 8)}`;
}

function time(value) {
	if (!value) return "none";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	return fmt.format(date);
}

function duration(run) {
	if (!run.started_at || !run.finished_at) return "";
	const ms =
		new Date(run.finished_at).getTime() - new Date(run.started_at).getTime();
	if (!Number.isFinite(ms) || ms < 0) return "";
	if (ms < 1000) return `${ms}ms`;
	return `${(ms / 1000).toFixed(2)}s`;
}

function escapeHTML(value) {
	return String(value ?? "")
		.replaceAll("&", "&amp;")
		.replaceAll("<", "&lt;")
		.replaceAll(">", "&gt;")
		.replaceAll('"', "&quot;")
		.replaceAll("'", "&#039;");
}

function jsonBlock(value) {
	return `<pre class="json-panel">${escapeHTML(JSON.stringify(value, null, 2))}</pre>`;
}

function statusClass(status) {
	if (status === "succeeded") return "good";
	if (["failed", "dead_lettered", "cancelled"].includes(status)) return "bad";
	if (["retry_scheduled", "pending", "claimed", "running"].includes(status))
		return "warn";
	return "info";
}

function chip(value, extra = "") {
	return `<span class="chip ${extra}">${escapeHTML(value || "none")}</span>`;
}

function targetSummary(schedule) {
	const target = schedule.target || {};
	if (target.kind === "http")
		return `${target.method || "GET"} ${target.url || ""}`.trim();
	if (target.kind === "shell")
		return [target.command, ...(target.args || [])].filter(Boolean).join(" ");
	if (target.kind === "workflow") return target.workflow_id || "workflow";
	return target.kind || "unknown";
}

function scheduleLabel(schedule) {
	return schedule
		? `${schedule.name} (${shortID(schedule.id)})`
		: "unknown schedule";
}

function showToast(message) {
	const toast = el("toast");
	toast.textContent = message;
	toast.classList.add("show");
	setTimeout(() => toast.classList.remove("show"), 3200);
}

async function copyText(text) {
	await navigator.clipboard.writeText(text);
	showToast("Copied");
}

async function loadStatus() {
	const [health, ready] = await Promise.all([
		fetch("/healthz")
			.then((res) => res.json())
			.catch(() => ({ ok: false })),
		fetch("/readyz")
			.then((res) => res.json())
			.catch(() => ({ ok: false, storage: "unknown" })),
	]);
	state.health = health;
	state.ready = ready;
	state.status = await api("/v1/status");
	renderPosture();
	renderStatus();
}

async function loadSchedules() {
	const body = await api("/v1/schedules?limit=200");
	state.schedules = body.items || [];
	renderScheduleFilter();
	renderSchedules();
}

function runQuery(reset) {
	const params = new URLSearchParams({ limit: "50" });
	const status = el("statusFilter").value;
	const scheduleID = el("scheduleFilter").value;
	const targetKind = el("targetFilter").value;
	if (status) params.set("status", status);
	if (scheduleID) params.set("schedule_id", scheduleID);
	if (targetKind) params.set("target_kind", targetKind);
	if (!reset && state.nextRunCursor) params.set("cursor", state.nextRunCursor);
	return `/v1/runs?${params.toString()}`;
}

async function loadRuns({ reset = true } = {}) {
	const body = await api(runQuery(reset));
	state.runs = reset
		? body.items || []
		: [...state.runs, ...(body.items || [])];
	state.nextRunCursor = body.next_cursor;
	renderRuns();
}

function renderPosture() {
	const s = state.status;
	if (!s) return;
	el("postureLine").textContent =
		`${s.version || "unknown"} · ${s.database.driver}:${s.database.path} · ` +
		`auth ${s.security.auth_required ? "required" : "off"} · audit ${s.security.request_audit ? "on" : "off"}`;
}

function renderScheduleFilter() {
	const select = el("scheduleFilter");
	const current = select.value;
	select.innerHTML = `<option value="">All schedules</option>${state.schedules
		.map(
			(s) =>
				`<option value="${escapeHTML(s.id)}">${escapeHTML(s.name)} · ${escapeHTML(shortID(s.id))}</option>`,
		)
		.join("")}`;
	select.value = current;
}

function renderRuns() {
	const container = el("runLedger");
	if (!state.runs.length) {
		container.innerHTML = `<div class="run-card"><p class="muted">No runs match the current filters.</p></div>`;
		return;
	}
	const head = `<div class="ledger-head">
		<div>Run</div><div>Schedule</div><div>Status</div><div>Try</div><div>Times</div><div>Duration</div><div>Target</div><div>Worker / Retry / Error</div>
	</div>`;
	const rows = state.runs
		.map((run) => {
			const err = run.error_text
				? escapeHTML(run.error_text.slice(0, 140))
				: "";
			const worker = run.claimed_by_worker_id
				? escapeHTML(run.claimed_by_worker_id)
				: "none";
			const retry = run.retry_available_at
				? `retry ${time(run.retry_available_at)}`
				: "";
			const scheduleText = `${run.schedule_name || "schedule"} · ${shortID(run.schedule_id)}`;
			const timeText = `created ${time(run.created_at)} · started ${time(run.started_at)} · finished ${time(run.finished_at)}`;
			return `<div class="run-row" role="button" tabindex="0" data-open-run="${escapeHTML(run.id)}">
				<span><button class="secondary short-id" type="button" data-copy-run="${escapeHTML(run.id)}">${escapeHTML(shortID(run.id))}</button></span>
				<span>${escapeHTML(scheduleText)}</span>
				<span>${chip(run.status, statusClass(run.status))}</span>
				<span class="mono">${escapeHTML(run.attempt)}</span>
				<span class="mono">${escapeHTML(timeText)}</span>
				<span class="mono">${escapeHTML(duration(run) || "open")}</span>
				<span>${chip(run.target_kind || "unknown")}</span>
				<span class="${err ? "error-preview" : "muted"}">${err || retry || `worker ${worker}`}</span>
			</div>
			<div class="run-card" data-open-run="${escapeHTML(run.id)}" role="button" tabindex="0">
				<div class="row-top"><button class="secondary short-id" type="button" data-copy-run="${escapeHTML(run.id)}">${escapeHTML(shortID(run.id))}</button>${chip(run.status, statusClass(run.status))}</div>
				<div class="record-line"><strong>${escapeHTML(scheduleText)}</strong>${chip(run.target_kind || "unknown")}<span class="mono">attempt ${escapeHTML(run.attempt)}</span></div>
				<div class="record-line muted"><span>created ${escapeHTML(time(run.created_at))}</span><span>started ${escapeHTML(time(run.started_at))}</span><span>finished ${escapeHTML(time(run.finished_at))}</span></div>
				<div class="record-line muted"><span>${escapeHTML(duration(run) || "open")}</span><span>worker ${worker}</span>${retry ? `<span>${escapeHTML(retry)}</span>` : ""}</div>
				${err ? `<div class="error-preview">${err}</div>` : ""}
			</div>`;
		})
		.join("");
	container.innerHTML = head + rows;
	container.querySelectorAll("[data-open-run]").forEach((node) => {
		node.addEventListener("click", () => openRun(node.dataset.openRun));
		node.addEventListener("keydown", (event) => {
			if (event.key === "Enter") openRun(node.dataset.openRun);
		});
	});
	container.querySelectorAll("[data-copy-run]").forEach((button) => {
		button.addEventListener("click", (event) => {
			event.stopPropagation();
			copyText(button.dataset.copyRun);
		});
	});
	el("loadMoreRuns").disabled = !state.nextRunCursor;
}

function renderSchedules() {
	const grid = el("scheduleGrid");
	if (!state.schedules.length) {
		grid.innerHTML = `<div class="schedule-card"><p class="muted">No schedules found.</p></div>`;
		return;
	}
	grid.innerHTML = state.schedules
		.map(
			(schedule) => `<article class="schedule-card">
			<div class="row-top">
				<h3>${escapeHTML(schedule.name)}</h3>
				${chip(schedule.enabled ? "enabled" : "paused", schedule.enabled ? "good" : "warn")}
			</div>
			<div class="chip-row">${chip(schedule.schedule.kind)}${chip(schedule.target_kind)}</div>
			<dl class="details">
				<dt>ID</dt><dd><button class="secondary mono" type="button" data-copy="${escapeHTML(schedule.id)}">${escapeHTML(shortID(schedule.id))}</button></dd>
				<dt>Timezone</dt><dd class="mono">${escapeHTML(schedule.timezone)}</dd>
				<dt>Next</dt><dd class="mono">${escapeHTML(time(schedule.next_run_at))}</dd>
				<dt>Last</dt><dd class="mono">${escapeHTML(time(schedule.last_run_at))}</dd>
			</dl>
			<div class="action-row">
				<button type="button" data-schedule-id="${escapeHTML(schedule.id)}" data-action="inspect">Inspect</button>
				<button class="secondary" type="button" data-schedule-id="${escapeHTML(schedule.id)}" data-action="${schedule.enabled ? "pause" : "resume"}">${schedule.enabled ? "Pause" : "Resume"}</button>
				<button class="secondary" type="button" data-schedule-id="${escapeHTML(schedule.id)}" data-action="trigger">Trigger</button>
			</div>
		</article>`,
		)
		.join("");
	grid.querySelectorAll("[data-copy]").forEach((button) => {
		button.addEventListener("click", () => copyText(button.dataset.copy));
	});
	grid.querySelectorAll("[data-action]").forEach((button) => {
		button.addEventListener("click", () =>
			scheduleAction(button.dataset.scheduleId, button.dataset.action),
		);
	});
}

function renderStatus() {
	if (!state.status) return;
	const s = state.status;
	const metrics = [
		[
			"Health",
			state.health?.ok ? "ok" : "error",
			state.ready?.storage || "storage unknown",
		],
		["Service", s.service, `version ${s.version || "unknown"}`],
		["Started", time(s.started_at), s.started_at],
		["Database", s.database.driver, s.database.path],
		[
			"Scheduler",
			`${s.scheduler.due_runs} due`,
			`last tick ${s.scheduler.last_tick_at || "none"}`,
		],
		[
			"Next due",
			s.scheduler.next_due_run_at ? time(s.scheduler.next_due_run_at) : "none",
			s.scheduler.next_due_schedule_id || "",
		],
		[
			"Workers",
			`${s.workers.active} active`,
			`${s.workers.claimed_but_expired} expired claims`,
		],
		[
			"Runs",
			`${s.runs.running} running`,
			`${s.runs.failed} failed · ${s.runs.retry_queued} retry · ${s.runs.dead_letter} dead-letter`,
		],
		[
			"Retention",
			`${s.retention.run_retention_days} days`,
			`${s.retention.receipt_max_bytes} receipt bytes`,
		],
		[
			"Security",
			s.security.auth_required ? "auth required" : "auth off",
			s.security.request_audit ? "audit enabled" : "audit off",
		],
	];
	el("statusGrid").innerHTML = metrics
		.map(
			([title, value, detail]) =>
				`<article class="metric"><h3>${escapeHTML(title)}</h3><p class="mono">${escapeHTML(value)}</p><p class="muted">${escapeHTML(detail)}</p></article>`,
		)
		.join("");
	el("statusJSON").textContent = JSON.stringify(
		{ health: state.health, ready: state.ready, status: state.status },
		null,
		2,
	);
}

async function openRun(runID) {
	const run = await api(`/v1/runs/${encodeURIComponent(runID)}`);
	const schedule =
		state.schedules.find((item) => item.id === run.schedule_id) ||
		(await api(`/v1/schedules/${encodeURIComponent(run.schedule_id)}`));
	openDrawer(
		"Run",
		shortID(run.id),
		`
		<div class="action-row"><button class="secondary mono" type="button" data-copy-run="${escapeHTML(run.id)}">Copy run id</button></div>
		<div class="chip-row">${chip(run.status, statusClass(run.status))}${chip(schedule.target?.kind || "unknown")}<span class="mono">attempt ${escapeHTML(run.attempt)}</span></div>
		<dl class="details">
			<dt>Schedule</dt><dd>${escapeHTML(scheduleLabel(schedule))}</dd>
			<dt>Occurrence</dt><dd class="mono">${escapeHTML(run.occurrence_key)}</dd>
			<dt>Nominal</dt><dd class="mono">${escapeHTML(time(run.nominal_time))}</dd>
			<dt>Due</dt><dd class="mono">${escapeHTML(time(run.due_time))}</dd>
			<dt>Worker</dt><dd class="mono">${escapeHTML(run.claimed_by_worker_id || "none")}</dd>
			<dt>HTTP / exit</dt><dd class="mono">${escapeHTML(run.http_status_code ?? "none")} / ${escapeHTML(run.exit_code ?? "none")}</dd>
			<dt>Retry at</dt><dd class="mono">${escapeHTML(time(run.retry_available_at))}</dd>
			<dt>Error</dt><dd class="mono">${escapeHTML(run.error_text || "none")}</dd>
		</dl>
		<h3>Status timeline</h3>
		<div class="timeline">
			${timelineItem("created", run.created_at)}
			${timelineItem("started", run.started_at)}
			${timelineItem("finished", run.finished_at)}
			${timelineItem("updated", run.updated_at)}
		</div>
		<h3>Attempt history</h3>
		${attemptsTable(run.attempts || [])}
		<h3>Receipts</h3>
		${receiptsList(run.receipts || [])}
		${run.dead_letter ? `<h3>Dead letter</h3><p class="error-preview">${escapeHTML(run.dead_letter.reason)}</p>${jsonBlock(run.dead_letter)}` : ""}
		<h3>Result JSON</h3>
		${jsonBlock(run.result_json ?? null)}
		<h3>Raw run JSON</h3>
		${jsonBlock(run)}
	`,
	);
	el("drawerBody")
		.querySelector("[data-copy-run]")
		?.addEventListener("click", () => copyText(run.id));
}

function timelineItem(label, value) {
	return `<div class="timeline-item"><strong>${escapeHTML(label)}</strong><div class="mono muted">${escapeHTML(time(value))}</div></div>`;
}

function attemptsTable(attempts) {
	if (!attempts.length) return `<p class="muted">No attempt history found.</p>`;
	return `<div class="ledger-table">${attempts
		.map(
			(a) => `<div class="run-card">
			<div class="row-top"><span class="mono">attempt ${escapeHTML(a.attempt)}</span>${chip(a.status, statusClass(a.status))}</div>
			<div class="record-line muted"><span>${escapeHTML(time(a.created_at))}</span><span>${escapeHTML(duration(a) || "open")}</span><span>${escapeHTML(a.error_text || "")}</span></div>
		</div>`,
		)
		.join("")}</div>`;
}

function receiptsList(receipts) {
	if (!receipts.length) return `<p class="muted">No receipts recorded.</p>`;
	return receipts
		.map(
			(receipt) => `<article class="schedule-card">
			<div class="row-top"><strong>${escapeHTML(receipt.receipt_kind)}</strong><span class="mono muted">${escapeHTML(time(receipt.created_at))}</span></div>
			<p class="muted">${escapeHTML(receipt.content_type || "text/plain")}</p>
			<pre class="receipt-body">${escapeHTML(receipt.body)}</pre>
		</article>`,
		)
		.join("");
}

async function scheduleAction(scheduleID, action) {
	if (action === "inspect") {
		await openSchedule(scheduleID);
		return;
	}
	if (action === "trigger") {
		const reason = prompt("Trigger reason", "operator console");
		if (reason === null) return;
		await api(`/v1/schedules/${encodeURIComponent(scheduleID)}/trigger`, {
			method: "POST",
			body: JSON.stringify({ reason }),
		});
		showToast("Schedule triggered");
		await refreshAll();
		return;
	}
	await api(`/v1/schedules/${encodeURIComponent(scheduleID)}/${action}`, {
		method: "POST",
	});
	showToast(`Schedule ${action}d`);
	await refreshAll();
}

async function openSchedule(scheduleID) {
	const schedule = await api(`/v1/schedules/${encodeURIComponent(scheduleID)}`);
	const runs = await api(
		`/v1/schedules/${encodeURIComponent(scheduleID)}/runs?limit=8`,
	);
	openDrawer(
		"Schedule",
		schedule.name,
		`
		<div class="action-row">
			<button class="secondary mono" type="button" data-copy-schedule="${escapeHTML(schedule.id)}">Copy schedule id</button>
			<button type="button" data-schedule-action="${schedule.enabled ? "pause" : "resume"}">${schedule.enabled ? "Pause" : "Resume"}</button>
			<button class="secondary" type="button" data-schedule-action="trigger">Trigger</button>
		</div>
		<div class="chip-row">${chip(schedule.enabled ? "enabled" : "paused", schedule.enabled ? "good" : "warn")}${chip(schedule.schedule.kind)}${chip(schedule.target.kind)}</div>
		<dl class="details">
			<dt>ID</dt><dd class="mono">${escapeHTML(schedule.id)}</dd>
			<dt>Timezone</dt><dd class="mono">${escapeHTML(schedule.timezone)}</dd>
			<dt>Expression</dt><dd class="mono">${escapeHTML(schedule.schedule.expr || schedule.schedule.every_seconds || schedule.schedule.at || "none")}</dd>
			<dt>Next run</dt><dd class="mono">${escapeHTML(time(schedule.next_run_at))}</dd>
			<dt>Last run</dt><dd class="mono">${escapeHTML(time(schedule.last_run_at))}</dd>
			<dt>Target</dt><dd>${escapeHTML(targetSummary(schedule))}</dd>
			<dt>Overlap</dt><dd class="mono">${escapeHTML(schedule.policy.overlap)}</dd>
			<dt>Misfire</dt><dd class="mono">${escapeHTML(schedule.policy.misfire)}</dd>
			<dt>Timeout</dt><dd class="mono">${escapeHTML(schedule.policy.timeout_seconds)}s</dd>
			<dt>Max concurrency</dt><dd class="mono">${escapeHTML(schedule.policy.max_concurrency)}</dd>
			<dt>Retry</dt><dd class="mono">${escapeHTML(schedule.retry.strategy)} · max ${escapeHTML(schedule.retry.max_attempts)}</dd>
		</dl>
		<h3>Recent runs</h3>
		${attemptsTable(runs.items || [])}
		<h3>Raw schedule JSON</h3>
		${jsonBlock(schedule)}
	`,
	);
	const body = el("drawerBody");
	body
		.querySelector("[data-copy-schedule]")
		?.addEventListener("click", () => copyText(schedule.id));
	body.querySelectorAll("[data-schedule-action]").forEach((button) => {
		button.addEventListener("click", () =>
			scheduleAction(schedule.id, button.dataset.scheduleAction),
		);
	});
}

function openDrawer(kicker, title, body) {
	el("drawerKicker").textContent = kicker;
	el("drawerTitle").textContent = title;
	el("drawerBody").innerHTML = body;
	el("drawer").classList.add("open");
	el("drawer").setAttribute("aria-hidden", "false");
}

function closeDrawer() {
	el("drawer").classList.remove("open");
	el("drawer").setAttribute("aria-hidden", "true");
}

async function refreshAll() {
	try {
		await loadStatus();
		await loadSchedules();
		await loadRuns({ reset: true });
	} catch (error) {
		showToast(error.message);
		if (error.message.includes("Auth required")) promptToken();
	}
}

function promptToken() {
	const next = prompt("Wakeplane operator bearer token", state.token);
	if (next === null) return;
	state.token = next.trim();
	if (state.token) localStorage.setItem("wakeplane.authToken", state.token);
	else localStorage.removeItem("wakeplane.authToken");
	refreshAll();
}

function bind() {
	el("refreshButton").addEventListener("click", refreshAll);
	el("tokenButton").addEventListener("click", promptToken);
	el("closeDrawer").addEventListener("click", closeDrawer);
	el("drawer").addEventListener("click", (event) => {
		if (event.target.id === "drawer") closeDrawer();
	});
	for (const id of ["statusFilter", "scheduleFilter", "targetFilter"]) {
		el(id).addEventListener("change", () =>
			loadRuns({ reset: true }).catch((error) => showToast(error.message)),
		);
	}
	el("loadMoreRuns").addEventListener("click", () =>
		loadRuns({ reset: false }).catch((error) => showToast(error.message)),
	);
	document.querySelectorAll(".tab").forEach((tab) => {
		tab.addEventListener("click", () => {
			state.view = tab.dataset.view;
			document.querySelectorAll(".tab").forEach((item) => {
				item.classList.toggle("active", item === tab);
			});
			document.querySelectorAll(".view").forEach((view) => {
				view.classList.toggle("active", view.id === `${state.view}View`);
			});
		});
	});
}

bind();
refreshAll();
