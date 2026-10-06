const state = {
	token: localStorage.getItem("wakeplane.authToken") || "",
	status: null,
	health: null,
	ready: null,
	schedules: [],
	runs: [],
	nextRunCursor: null,
	view: "ledger",
	templates: [],
	editingSchedule: null,
	formBusy: false,
	drawerOpener: null,
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
	let body = null;
	try {
		body = text ? JSON.parse(text) : null;
	} catch {
		throw new Error(
			`The service returned an unreadable response (${res.status}).`,
		);
	}
	if (!res.ok) {
		const details = (body?.details || [])
			.map((detail) => `${detail.field}: ${detail.message}`)
			.join("; ");
		throw new Error(
			details || body?.error || `${res.status} ${res.statusText}`,
		);
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
		return `${target.http_job ? "Tracked job · " : ""}${target.method || "GET"} ${target.url || ""}`.trim();
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

async function loadTemplates() {
	try {
		const body = await api("/v1/templates");
		state.templates = body.items || [];
		el("templateCards").innerHTML = state.templates
			.map(
				(template) =>
					`<button class="template-card secondary" type="button" data-template="${escapeHTML(template.id)}"><strong>${escapeHTML(template.name)}</strong><span>${escapeHTML(template.description)}</span><span class="template-link">Use template →</span></button>`,
			)
			.join("");
		el("templateCards")
			.querySelectorAll("[data-template]")
			.forEach((button) => {
				button.addEventListener("click", () => {
					const template = state.templates.find(
						(item) => item.id === button.dataset.template,
					);
					if (template) openScheduleForm(null, template);
				});
			});
	} catch (error) {
		el("templateCards").textContent = `Templates unavailable: ${error.message}`;
	}
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
		container.innerHTML = `<div class="run-card empty-state"><p class="muted">No runs match the current filters.</p></div>`;
		el("loadMoreRuns").disabled = !state.nextRunCursor;
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
				<span class="${err ? "error-preview" : "muted"}">${err || retry || runOutcome(run, worker)}</span>
			</div>
			<div class="run-card" data-open-run="${escapeHTML(run.id)}" role="button" tabindex="0">
				<div class="row-top"><button class="secondary short-id" type="button" data-copy-run="${escapeHTML(run.id)}">${escapeHTML(shortID(run.id))}</button>${chip(run.status, statusClass(run.status))}</div>
				<div class="record-line"><strong>${escapeHTML(scheduleText)}</strong>${chip(run.target_kind || "unknown")}<span class="mono">attempt ${escapeHTML(run.attempt)}</span></div>
				<div class="record-line muted"><span>created ${escapeHTML(time(run.created_at))}</span><span>started ${escapeHTML(time(run.started_at))}</span><span>finished ${escapeHTML(time(run.finished_at))}</span></div>
				<div class="record-line muted"><span>${escapeHTML(duration(run) || "open")}</span><span>worker ${worker}</span>${retry ? `<span>${escapeHTML(retry)}</span>` : ""}</div>
				${err ? `<div class="error-preview">${err}</div>` : ""}
				${run.external_job ? `<div class="record-line">${runOutcome(run, worker)}</div>` : ""}
			</div>`;
		})
		.join("");
	container.innerHTML = head + rows;
	container.querySelectorAll("[data-open-run]").forEach((node) => {
		node.addEventListener("click", () =>
			openRun(node.dataset.openRun).catch((error) => showToast(error.message)),
		);
		node.addEventListener("keydown", (event) => {
			if (event.key === "Enter" || event.key === " ") {
				if (event.target !== node) return;
				event.preventDefault();
				openRun(node.dataset.openRun).catch((error) =>
					showToast(error.message),
				);
			}
		});
	});
	container.querySelectorAll("[data-copy-run]").forEach((button) => {
		button.addEventListener("click", (event) => {
			event.stopPropagation();
			copyText(button.dataset.copyRun).catch((error) =>
				showToast(error.message),
			);
		});
	});
	el("loadMoreRuns").disabled = !state.nextRunCursor;
}

function renderSchedules() {
	const grid = el("scheduleGrid");
	if (!state.schedules.length) {
		grid.innerHTML = `<div class="schedule-card empty-state"><h3>Your first automation starts here</h3><p class="muted">Choose a template above or create an automation. Save it paused, preview the timing, then enable it when ready.</p></div>`;
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
				<button class="secondary" type="button" data-schedule-id="${escapeHTML(schedule.id)}" data-action="edit">Edit</button>
				<button class="secondary" type="button" data-schedule-id="${escapeHTML(schedule.id)}" data-action="${schedule.enabled ? "pause" : "resume"}">${schedule.enabled ? "Pause" : "Resume"}</button>
				<button class="secondary" type="button" data-schedule-id="${escapeHTML(schedule.id)}" data-action="trigger">Trigger</button>
			</div>
		</article>`,
		)
		.join("");
	grid.querySelectorAll("[data-copy]").forEach((button) => {
		button.addEventListener("click", () =>
			copyText(button.dataset.copy).catch((error) => showToast(error.message)),
		);
	});
	grid.querySelectorAll("[data-action]").forEach((button) => {
		button.addEventListener("click", () =>
			scheduleAction(button.dataset.scheduleId, button.dataset.action).catch(
				(error) => showToast(error.message),
			),
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
		<div class="action-row"><button class="secondary" type="button" data-refresh-run>Refresh run</button><button class="secondary mono" type="button" data-copy-run="${escapeHTML(run.id)}">Copy run id</button></div>
		<div class="chip-row">${chip(run.status, statusClass(run.status))}${chip(schedule.target?.kind || schedule.target_kind || "unknown")}<span class="mono">attempt ${escapeHTML(run.attempt)}</span></div>
		${canReconcileExternalJob(run) ? `<div class="recovery-panel"><p>The local run ended while the runner's outcome was still unconfirmed. Check its actual status to record evidence and release its overlap reservation when finished.</p><button class="secondary" type="button" data-reconcile-run>Check runner status</button><p class="field-hint">This observes existing work. It does not start or cancel a job.</p></div>` : ""}
		${externalJobPanel(run.external_job)}
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
		?.addEventListener("click", () =>
			copyText(run.id).catch((error) => showToast(error.message)),
		);
	el("drawerBody")
		.querySelector("[data-refresh-run]")
		?.addEventListener("click", () =>
			openRun(run.id).catch((error) => showToast(error.message)),
		);
	el("drawerBody")
		.querySelector("[data-reconcile-run]")
		?.addEventListener("click", async (event) => {
			const button = event.currentTarget;
			button.disabled = true;
			button.textContent = "Checking runner…";
			try {
				const observed = await api(
					`/v1/runs/${encodeURIComponent(run.id)}/reconcile`,
					{ method: "POST" },
				);
				await openRun(run.id);
				showToast(
					`Runner status recorded: ${observed.external_job?.status || "observed"}. Receipt added.`,
				);
			} catch (error) {
				showToast(error.message);
				button.disabled = false;
				button.textContent = "Check runner status";
			}
		});
	if (updateCachedRun(run, el("statusFilter").value)) {
		const openerRun =
			state.drawerOpener?.closest("[data-open-run]")?.dataset.openRun;
		renderRuns();
		if (openerRun && !state.drawerOpener.isConnected) {
			state.drawerOpener =
				[...el("runLedger").querySelectorAll("[data-open-run]")].find(
					(node) =>
						node.dataset.openRun === openerRun &&
						node.getClientRects().length > 0,
				) || el("statusFilter");
		}
	}
}

function updateCachedRun(run, statusFilter = "") {
	const index = state.runs.findIndex((item) => item.id === run.id);
	if (index < 0) return false;
	// Run details omit schedule labels and target_kind supplied by the ledger API.
	state.runs[index] = { ...state.runs[index], ...run };
	if (statusFilter)
		state.runs = state.runs.filter((item) => item.status === statusFilter);
	return true;
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
	if (action === "edit") {
		const schedule = await api(
			`/v1/schedules/${encodeURIComponent(scheduleID)}`,
		);
		closeDrawer();
		openScheduleForm(schedule);
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
			<button class="secondary" type="button" data-schedule-action="edit">Edit automation</button>
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
		?.addEventListener("click", () =>
			copyText(schedule.id).catch((error) => showToast(error.message)),
		);
	body.querySelectorAll("[data-schedule-action]").forEach((button) => {
		button.addEventListener("click", () =>
			scheduleAction(schedule.id, button.dataset.scheduleAction).catch(
				(error) => showToast(error.message),
			),
		);
	});
}

function openDrawer(kicker, title, body) {
	if (!el("drawer").classList.contains("open"))
		state.drawerOpener = document.activeElement;
	el("drawerKicker").textContent = kicker;
	el("drawerTitle").textContent = title;
	el("drawerBody").innerHTML = body;
	el("drawer").classList.add("open");
	el("drawer").setAttribute("aria-hidden", "false");
	el("drawer").inert = false;
	document.querySelector("main").inert = true;
	document.querySelector("header").inert = true;
	el("closeDrawer").focus();
}

function closeDrawer() {
	el("drawer").classList.remove("open");
	el("drawer").setAttribute("aria-hidden", "true");
	el("drawer").inert = true;
	document.querySelector("main").inert = false;
	document.querySelector("header").inert = false;
	findDrawerReturnFocus(state.drawerOpener)?.focus();
}

function findDrawerReturnFocus(opener) {
	const usable = (node) =>
		node?.isConnected &&
		!node.disabled &&
		node.getClientRects().length > 0 &&
		!node.closest("[inert]");
	if (usable(opener)) return opener;
	const runID = opener?.closest("[data-open-run]")?.dataset.openRun;
	if (runID) {
		const row = [...document.querySelectorAll("[data-open-run]")].find(
			(node) => node.dataset.openRun === runID && usable(node),
		);
		if (row) return row;
	}
	for (const candidate of [
		el("statusFilter"),
		document.querySelector(".tab.active"),
	]) {
		if (usable(candidate)) return candidate;
	}
	return null;
}

function safeURL(value) {
	try {
		const url = new URL(value);
		return ["https:", "http:"].includes(url.protocol) ? url.href : null;
	} catch {
		return null;
	}
}

function canReconcileExternalJob(run) {
	const job = run.external_job;
	if (typeof job?.can_reconcile === "boolean") return job.can_reconcile;
	return Boolean(
		job?.job_id &&
			!job.compacted &&
			["submitting", "queued", "running"].includes(job.status) &&
			["succeeded", "failed", "dead_lettered", "cancelled", "skipped"].includes(
				run.status,
			),
	);
}

function runOutcome(run, worker) {
	const job = run.external_job;
	if (!job) return `worker ${worker}`;
	const percent = Number(job.progress?.percent);
	const progress = Number.isFinite(percent) ? ` · ${percent}%` : "";
	return `${escapeHTML(job.progress?.message || `job ${job.status}`)}${escapeHTML(progress)}`;
}

function externalJobPanel(job) {
	if (!job) return "";
	const percent = Number(job.progress?.percent);
	const artifacts = (job.artifacts || [])
		.map((artifact) => {
			const url = safeURL(artifact.url);
			return url
				? `<li><a href="${escapeHTML(url)}" target="_blank" rel="noopener noreferrer">${escapeHTML(artifact.name || "Open output")} ↗</a>${artifact.content_type ? ` <span class="muted">${escapeHTML(artifact.content_type)}</span>` : ""}</li>`
				: "";
		})
		.join("");
	return `<section class="outcome-panel" aria-label="External job outcome">
		<div class="row-top"><h3>Runner outcome</h3>${chip(job.status, statusClass(job.status))}</div>
		<p>${escapeHTML(job.progress?.message || "Wakeplane follows this job until the runner reports completion.")}</p>
		${Number.isFinite(percent) ? `<progress max="100" value="${Math.max(0, Math.min(100, percent))}" aria-label="Job progress">${escapeHTML(percent)}%</progress>` : ""}
		<dl class="details"><dt>Job</dt><dd class="mono">${escapeHTML(job.job_id || "awaiting submission")}</dd><dt>Last update</dt><dd>${escapeHTML(time(job.updated_at))}</dd><dt>Deadline</dt><dd>${escapeHTML(time(job.deadline_at))}</dd></dl>
		${job.error ? `<p class="form-error">${escapeHTML(job.error)}</p>` : ""}
		${artifacts ? `<h4>Outputs</h4><ul class="artifact-list">${artifacts}</ul>` : ""}
		${job.result !== undefined && job.result !== null ? reportResultPanel(job.result) : ""}
	</section>`;
}

function reportResultPanel(result) {
	if (!result || typeof result !== "object" || !Array.isArray(result.items))
		return `<h4>Result</h4>${jsonBlock(result)}`;
	const items = result.items
		.filter((item) => item && typeof item === "object")
		.map((item) => {
			const url = safeURL(item.url);
			const title = escapeHTML(item.title || "Result item");
			return `<li>${url ? `<a href="${escapeHTML(url)}" target="_blank" rel="noopener noreferrer">${title} ↗</a>` : `<strong>${title}</strong>`}${item.source ? `<span class="field-hint">${escapeHTML(item.source)}${item.published_at ? ` · ${escapeHTML(item.published_at)}` : ""}</span>` : ""}${item.excerpt ? `<p>${escapeHTML(item.excerpt)}</p>` : ""}</li>`;
		})
		.join("");
	const delivery = result.delivery;
	return `<h4>${escapeHTML(result.title || "Result")}</h4>${result.summary ? `<p>${escapeHTML(result.summary)}</p>` : ""}${delivery ? `<div class="delivery-state">Notification ${chip(delivery.status, delivery.status === "sent" ? "good" : delivery.status === "failed" ? "bad" : "warn")}<span class="field-hint">${escapeHTML(delivery.attempts || 0)} delivery attempts${delivery.last_error ? ` · ${escapeHTML(delivery.last_error)}` : ""}</span></div>` : ""}<ul class="report-items">${items}</ul><details><summary>Result details</summary>${jsonBlock(result)}</details>`;
}

function inputField(id, label, value = "", options = {}) {
	const { type = "text", hint = "", ...attributes } = options;
	const attrs = Object.entries(attributes)
		.map(([name, val]) => `${name}="${escapeHTML(val)}"`)
		.join(" ");
	return `<label for="${id}">${escapeHTML(label)}<input id="${id}" name="${id}" type="${type}" value="${escapeHTML(value ?? "")}" ${attrs}${hint ? ` aria-describedby="${id}Hint"` : ""}>${hint ? `<span id="${id}Hint" class="field-hint">${escapeHTML(hint)}</span>` : ""}</label>`;
}

function selectField(id, label, value, choices) {
	return `<label for="${id}">${escapeHTML(label)}<select id="${id}" name="${id}">${choices
		.map(
			([key, title]) =>
				`<option value="${escapeHTML(key)}"${String(value) === String(key) ? " selected" : ""}>${escapeHTML(title)}</option>`,
		)
		.join("")}</select></label>`;
}

function jsonField(id, label, value, hint) {
	return `<label for="${id}">${escapeHTML(label)}<textarea id="${id}" name="${id}" rows="4" spellcheck="false" aria-describedby="${id}Hint">${escapeHTML(JSON.stringify(value, null, 2))}</textarea><span id="${id}Hint" class="field-hint">${escapeHTML(hint)}</span></label>`;
}

function defaultScheduleRequest() {
	return {
		name: "",
		enabled: false,
		timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
		schedule: { kind: "cron", expr: "0 9 * * 1" },
		target: { kind: "http", method: "POST", url: "", body: {} },
		policy: {
			overlap: "forbid",
			misfire: "run_once_if_late",
			timeout_seconds: 300,
			max_concurrency: 1,
		},
		retry: {
			max_attempts: 1,
			strategy: "none",
			initial_delay_seconds: 10,
			max_delay_seconds: 300,
		},
		start_at: null,
		end_at: null,
	};
}

function cronPreset(expr) {
	const parts = String(expr || "").split(" ");
	const [minute, hour, day, month, weekday] = parts;
	if (
		parts.length === 5 &&
		/^\d+$/.test(minute) &&
		/^\d+$/.test(hour) &&
		Number(minute) < 60 &&
		Number(hour) < 24 &&
		day === "*" &&
		month === "*"
	) {
		const clock = `${hour.padStart(2, "0")}:${minute.padStart(2, "0")}`;
		if (weekday === "*") return { frequency: "daily", clock, weekday: "1" };
		if (weekday === "1-5")
			return { frequency: "weekdays", clock, weekday: "1" };
		if (/^[0-6]$/.test(weekday)) return { frequency: "weekly", clock, weekday };
	}
	return { frequency: "custom", clock: "09:00", weekday: "1" };
}

function openScheduleForm(schedule = null, template = null) {
	state.editingSchedule = schedule;
	const req = schedule || template?.request || defaultScheduleRequest();
	const spec = req.schedule;
	const target = req.target;
	const policy = req.policy;
	const retry = req.retry;
	const preset = cronPreset(spec.expr);
	el("scheduleFormTitle").textContent = schedule
		? "Edit automation"
		: template?.name || "Create automation";
	el("scheduleFormError").textContent = "";
	el("scheduleFormFields").innerHTML = `
		${template ? `<p class="form-intro">${escapeHTML(template.description)}</p>` : ""}
		<fieldset><legend>1. Name and timing</legend><div class="form-grid">
			${inputField("automationName", "Name", req.name, { required: "", maxlength: "200", placeholder: "Weekly project summary" })}
			${inputField("automationTimezone", "Timezone", req.timezone, { required: "", placeholder: "America/Los_Angeles", hint: "A named timezone keeps recurring times aligned when daylight saving changes." })}
			${selectField("automationKind", "When to run", spec.kind, [
				["cron", "At a regular time"],
				["interval", "Every set amount of time"],
				["once", "One time only"],
			])}
		</div>
		<div data-schedule-fields="cron" class="form-grid conditional-fields">
			${selectField("automationFrequency", "Repeat", preset.frequency, [
				["daily", "Every day"],
				["weekdays", "Weekdays"],
				["weekly", "Every week"],
				["custom", "Custom cron expression"],
			])}
			${selectField("automationWeekday", "Day", preset.weekday, [
				["1", "Monday"],
				["2", "Tuesday"],
				["3", "Wednesday"],
				["4", "Thursday"],
				["5", "Friday"],
				["6", "Saturday"],
				["0", "Sunday"],
			])}
			${inputField("automationClock", "Time in the selected timezone", preset.clock, { type: "time", required: "" })}
			${inputField("automationCron", "Cron expression", spec.expr || "0 9 * * 1", { required: "", hint: "Five fields: minute, hour, day of month, month, day of week." })}
		</div>
		<div data-schedule-fields="interval" class="form-grid conditional-fields">
			${inputField("automationInterval", "Repeat every (seconds)", spec.every_seconds || 3600, { type: "number", required: "", min: "1", step: "1", hint: "60 = one minute, 3600 = one hour, 86400 = one day." })}
			${inputField("automationAnchor", "First interval starts at (optional)", spec.anchor_at, { hint: "Use an explicit offset, for example 2026-10-08T09:00:00-07:00." })}
		</div>
		<div data-schedule-fields="once" class="conditional-fields">${inputField("automationOnce", "Run at", spec.at, { required: "", placeholder: "2026-10-08T09:00:00-07:00", hint: "Include a timezone offset or Z for UTC. Preview confirms the exact time." })}</div>
		</fieldset>
		<fieldset><legend>2. Work to perform</legend>
		${selectField("automationTarget", "Run using", target.kind, [
			["http", "Your service or job runner"],
			["shell", "A command on this machine"],
			["workflow", "A registered workflow"],
		])}
		<div data-target-fields="http" class="conditional-fields">
			<div class="form-grid">${inputField("automationURL", "Service address", target.url, { type: "url", required: "", placeholder: "https://your-service.example/jobs", hint: "Connect a service you operate. Connected accounts and app actions belong to that service." })}${selectField(
				"automationMethod",
				"Request method",
				target.method || "POST",
				[
					["POST", "POST — start work"],
					["GET", "GET — fetch information"],
					["PUT", "PUT — replace"],
					["PATCH", "PATCH — update"],
					["DELETE", "DELETE — remove"],
					["HEAD", "HEAD — check availability"],
					["OPTIONS", "OPTIONS"],
				],
			)}</div>
			<label class="checkbox-label"><input id="automationTrackJob" type="checkbox"${target.http_job ? " checked" : ""}><span>Track the job until it finishes</span></label>
			<p class="field-hint">For runners implementing Wakeplane's job contract. Accepted work stays open until the runner reports success, failure, or cancellation.</p>
			<div id="jobPollingFields" class="form-grid">${inputField("automationPoll", "Check progress every (seconds)", target.http_job?.poll_interval_seconds || 5, { type: "number", min: "1", max: "300", step: "1", required: "" })}${inputField("automationLookup", "Recovery lookup address (optional)", target.http_job?.lookup_url, { type: "url", hint: "Runner endpoint that finds accepted work by its Idempotency-Key. Must share the service address's origin." })}</div>
			<div id="runnerTaskFields" class="runner-task-fields">
				<h4>Set up this task</h4>
				<div data-recipe-fields="repository-watch">${inputField("automationRepository", "GitHub repository", target.body?.repository, { required: "", maxlength: "200", placeholder: "justyn-clark/wakeplane", hint: "Owner/name, without a full web address or .git suffix. Reads repository activity, latest stable release, and open pull requests." })}</div>
				<div data-recipe-fields="weekly-summary"><label for="automationFeeds">Feed addresses — one per line<textarea id="automationFeeds" name="automationFeeds" rows="3" spellcheck="false" required aria-describedby="automationFeedsHint">${escapeHTML(Array.isArray(target.body?.feeds) ? target.body.feeds.join("\n") : "")}</textarea><span id="automationFeedsHint" class="field-hint">Add up to five RSS or Atom feed addresses. The report includes the twenty latest source excerpts and links.</span></label></div>
				${inputField("automationNotify", "Notification receiver (optional)", target.body?.notify_url, { type: "url", placeholder: "https://your-service.example/notifications", hint: "Your runner sends the completed report to this webhook. Leave blank to read the result in Wakeplane." })}
			</div>
			<details id="advancedBody" class="advanced-fields"${["repository-watch", "weekly-summary"].includes(target.body?.task) ? "" : " open"}><summary>Advanced service instructions</summary>${jsonField("automationBody", "Instructions to your service (JSON object)", target.body || {}, "Your runner decides what these instructions mean. Other settings are preserved when you use the guided task fields.")}</details>
			<details class="advanced-fields"><summary>Request headers</summary>${jsonField("automationHeaders", "Headers (JSON object)", target.headers || {}, "Use your runner's managed accounts for application access. Headers are stored with this schedule.")}</details>
		</div>
		<div data-target-fields="shell" class="conditional-fields">
			${inputField("automationCommand", "Command", target.command, { required: "", placeholder: "/usr/local/bin/backup", hint: "Runs directly on the Wakeplane host. Arguments are passed individually; shell syntax is not expanded." })}
			${jsonField("automationArgs", "Arguments (JSON array)", target.args || [], 'Example: ["--destination", "/backups"]. Each item must be text.')}
		</div>
		<div data-target-fields="workflow" class="conditional-fields">
			${inputField("automationWorkflow", "Registered workflow ID", target.workflow_id, { required: "", hint: "The workflow must be registered in this Wakeplane deployment." })}
			${jsonField("automationInput", "Workflow input (JSON object)", target.input || {}, "Provide the input expected by the registered workflow.")}
		</div>
		</fieldset>
		<fieldset><legend>3. Safety and recovery</legend>
		<p class="field-hint">Defaults prevent overlapping work. Retries can repeat side effects; enable them only when your service handles repeated requests safely.</p>
		<div class="form-grid">
			${selectField(
				"automationOverlap",
				"If earlier work is still running",
				policy.overlap,
				[
					["forbid", "Skip the new occurrence"],
					["queue_latest", "Queue only the newest occurrence"],
					["allow", "Allow overlap"],
					["replace", "Replace the earlier run"],
				],
			)}
			${selectField(
				"automationMisfire",
				"If Wakeplane was offline",
				policy.misfire,
				[
					["run_once_if_late", "Run once when back online"],
					["skip", "Skip missed occurrences"],
					["catch_up", "Catch up missed occurrences"],
				],
			)}
			${inputField("automationTimeout", "Time limit (seconds)", policy.timeout_seconds, { type: "number", required: "", min: "1", step: "1" })}
			${inputField("automationConcurrency", "Maximum simultaneous runs", policy.max_concurrency, { type: "number", required: "", min: "1", step: "1" })}
			${selectField("automationRetry", "On failure", retry.strategy, [
				["none", "Do not retry automatically"],
				["exponential", "Retry with increasing delay"],
			])}
		</div>
		<div id="retryFields" class="form-grid conditional-fields">
			${inputField("automationAttempts", "Maximum attempts", retry.max_attempts || 1, { type: "number", required: "", min: "1", step: "1" })}
			${inputField("automationRetryDelay", "First retry delay (seconds)", retry.initial_delay_seconds || 10, { type: "number", required: "", min: "1", step: "1" })}
			${inputField("automationMaxDelay", "Longest retry delay (seconds)", retry.max_delay_seconds || 300, { type: "number", required: "", min: "1", step: "1" })}
		</div>
		<details class="advanced-fields"><summary>Limit the active date range</summary><div class="form-grid">
			${inputField("automationStart", "Start at (optional)", req.start_at, { hint: "Use an explicit offset or Z for UTC." })}
			${inputField("automationEnd", "End at (optional)", req.end_at, { hint: "Use an explicit offset or Z for UTC." })}
		</div></details>
		<label class="checkbox-label"><input id="automationEnabled" type="checkbox"${schedule?.enabled ? " checked" : ""}><span>Enable after saving</span></label>
		<p class="field-hint">Paused automations do not run on a schedule. You can inspect them and trigger a test run before enabling.</p>
		</fieldset>
		<section id="schedulePreview" class="preview-panel" aria-live="polite"><h3>Check the timing before saving</h3><p class="muted">Preview shows the next occurrences in your chosen timezone. It does not run any work.</p></section>`;
	for (const id of [
		"automationKind",
		"automationFrequency",
		"automationTarget",
		"automationTrackJob",
		"automationRetry",
	]) {
		el(id).addEventListener("change", updateFormFields);
	}
	el("automationBody").addEventListener("input", () => {
		syncTaskFieldsFromJSON();
		updateFormFields();
	});
	for (const id of [
		"automationRepository",
		"automationFeeds",
		"automationNotify",
	]) {
		el(id).addEventListener("input", syncJSONFromTaskFields);
	}
	updateFormFields();
	el("scheduleDialog").showModal();
	el("automationName").focus();
}

function setFieldsVisible(container, visible) {
	container.hidden = !visible;
	container.querySelectorAll("input, select, textarea").forEach((input) => {
		input.disabled = !visible || state.formBusy;
	});
}

function updateFormFields() {
	document.querySelectorAll("[data-schedule-fields]").forEach((node) => {
		setFieldsVisible(
			node,
			node.dataset.scheduleFields === el("automationKind").value,
		);
	});
	document.querySelectorAll("[data-target-fields]").forEach((node) => {
		setFieldsVisible(
			node,
			node.dataset.targetFields === el("automationTarget").value,
		);
	});
	const cron = el("automationKind").value === "cron";
	const frequency = el("automationFrequency").value;
	for (const [id, visible] of [
		["automationWeekday", cron && frequency === "weekly"],
		["automationClock", cron && frequency !== "custom"],
		["automationCron", cron && frequency === "custom"],
	]) {
		setFieldsVisible(el(id).closest("label"), visible);
	}
	const tracked =
		el("automationTarget").value === "http" && el("automationTrackJob").checked;
	setFieldsVisible(el("jobPollingFields"), tracked);
	if (tracked) el("automationMethod").value = "POST";
	el("automationMethod").disabled =
		tracked || state.formBusy || el("automationTarget").value !== "http";
	let task = "";
	try {
		task = JSON.parse(el("automationBody").value)?.task;
	} catch {
		// The preview reports invalid JSON.
	}
	const knownTask =
		el("automationTarget").value === "http" &&
		["repository-watch", "weekly-summary"].includes(task);
	setFieldsVisible(el("runnerTaskFields"), knownTask);
	document.querySelectorAll("[data-recipe-fields]").forEach((node) => {
		setFieldsVisible(node, knownTask && node.dataset.recipeFields === task);
	});
	setFieldsVisible(
		el("retryFields"),
		el("automationRetry").value === "exponential",
	);
}

function syncTaskFieldsFromJSON() {
	try {
		const body = JSON.parse(el("automationBody").value);
		el("automationRepository").value = body.repository || "";
		el("automationFeeds").value = Array.isArray(body.feeds)
			? body.feeds.join("\n")
			: "";
		el("automationNotify").value = body.notify_url || "";
	} catch {
		// Keep the fields intact while an incomplete JSON edit is in progress.
	}
}

function syncJSONFromTaskFields() {
	try {
		const body = JSON.parse(el("automationBody").value);
		if (body.task === "repository-watch")
			body.repository = el("automationRepository").value.trim();
		if (body.task === "weekly-summary")
			body.feeds = el("automationFeeds")
				.value.split("\n")
				.map((value) => value.trim())
				.filter(Boolean);
		if (["repository-watch", "weekly-summary"].includes(body.task))
			body.notify_url = el("automationNotify").value.trim();
		el("automationBody").value = JSON.stringify(body, null, 2);
	} catch {
		// The preview reports invalid JSON without losing the text.
	}
}

function readTaskFields(body) {
	if (!["repository-watch", "weekly-summary"].includes(body.task)) return body;
	const request = { ...body };
	if (body.task === "repository-watch") {
		request.repository = el("automationRepository").value.trim();
		if (
			!/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9_.-]+$/.test(request.repository) ||
			/\/(?:\.|\.\.)$/.test(request.repository)
		)
			throw new Error(
				"GitHub repository needs an owner/name, for example justyn-clark/wakeplane.",
			);
	} else {
		request.feeds = el("automationFeeds")
			.value.split("\n")
			.map((value) => value.trim())
			.filter(Boolean);
		if (!request.feeds.length || request.feeds.length > 5)
			throw new Error("Add between one and five feed addresses, one per line.");
		for (const address of request.feeds)
			validateRunnerAddress(address, "Feed address");
	}
	request.notify_url = el("automationNotify").value.trim();
	if (request.notify_url)
		validateRunnerAddress(request.notify_url, "Notification receiver");
	return request;
}

function validateRunnerAddress(value, label) {
	let parsed;
	try {
		parsed = new URL(value);
	} catch {
		throw new Error(`${label} must be a full HTTP or HTTPS address.`);
	}
	if (
		!safeURL(value) ||
		parsed.username ||
		parsed.password ||
		parsed.hash ||
		value.length > 2048
	)
		throw new Error(
			`${label} must be an HTTP or HTTPS address without embedded credentials or a fragment.`,
		);
}

function invalidatePreview() {
	el("schedulePreview").innerHTML =
		`<h3>Timing preview</h3><p class="muted">Preview again after changing the automation.</p>`;
}

function readJSONField(id, label, kind = "object") {
	let value;
	try {
		value = JSON.parse(el(id).value);
	} catch {
		throw new Error(`${label} must contain valid JSON.`);
	}
	if (kind === "array") {
		if (!Array.isArray(value) || value.some((item) => typeof item !== "string"))
			throw new Error(`${label} must be an array of text values.`);
	} else if (!value || Array.isArray(value) || typeof value !== "object") {
		throw new Error(`${label} must be a JSON object.`);
	}
	return value;
}

function readPositiveInteger(id, label) {
	const value = Number(el(id).value);
	if (!Number.isSafeInteger(value) || value < 1)
		throw new Error(`${label} must be a positive whole number.`);
	return value;
}

function readTimestamp(id, label, required = false) {
	const value = el(id).value.trim();
	if (!value && !required) return null;
	if (
		!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:\d{2})$/.test(
			value,
		) ||
		Number.isNaN(Date.parse(value))
	)
		throw new Error(
			`${label} needs a valid timestamp with an explicit offset or Z, for example 2026-10-08T09:00:00-07:00.`,
		);
	return value;
}

function readScheduleForm() {
	const kind = el("automationKind").value;
	const schedule = { kind };
	if (kind === "cron") {
		const frequency = el("automationFrequency").value;
		const [hour, minute] = el("automationClock").value.split(":").map(Number);
		schedule.expr =
			frequency === "custom"
				? el("automationCron").value.trim()
				: `${minute} ${hour} * * ${frequency === "daily" ? "*" : frequency === "weekdays" ? "1-5" : el("automationWeekday").value}`;
	} else if (kind === "interval") {
		schedule.every_seconds = readPositiveInteger(
			"automationInterval",
			"Repeat interval",
		);
		const anchor = readTimestamp("automationAnchor", "First interval");
		if (anchor) schedule.anchor_at = anchor;
	} else {
		schedule.at = readTimestamp("automationOnce", "Run at", true);
	}
	const targetKind = el("automationTarget").value;
	let target = { kind: targetKind };
	if (state.editingSchedule?.target.kind === targetKind)
		target = { ...state.editingSchedule.target };
	if (targetKind === "http") {
		target.method = el("automationMethod").value;
		target.url = el("automationURL").value.trim();
		target.body = readTaskFields(
			readJSONField("automationBody", "Instructions"),
		);
		target.headers = readJSONField("automationHeaders", "Headers");
		if (
			Object.values(target.headers).some((value) => typeof value !== "string")
		)
			throw new Error("Header values must be text.");
		if (el("automationTrackJob").checked) {
			target.method = "POST";
			target.http_job = {
				poll_interval_seconds: readPositiveInteger(
					"automationPoll",
					"Progress interval",
				),
			};
			const lookup = el("automationLookup").value.trim();
			if (lookup) target.http_job.lookup_url = lookup;
		} else {
			delete target.http_job;
		}
	} else if (targetKind === "shell") {
		target.command = el("automationCommand").value.trim();
		target.args = readJSONField("automationArgs", "Arguments", "array");
	} else {
		target.workflow_id = el("automationWorkflow").value.trim();
		target.input = readJSONField("automationInput", "Workflow input");
	}
	const strategy = el("automationRetry").value;
	return {
		name: el("automationName").value.trim(),
		enabled: el("automationEnabled").checked,
		timezone: el("automationTimezone").value.trim(),
		schedule,
		target,
		policy: {
			overlap: el("automationOverlap").value,
			misfire: el("automationMisfire").value,
			timeout_seconds: readPositiveInteger("automationTimeout", "Time limit"),
			max_concurrency: readPositiveInteger(
				"automationConcurrency",
				"Maximum simultaneous runs",
			),
		},
		retry: {
			strategy,
			max_attempts:
				strategy === "none"
					? 1
					: readPositiveInteger("automationAttempts", "Maximum attempts"),
			initial_delay_seconds:
				strategy === "none"
					? 0
					: readPositiveInteger("automationRetryDelay", "First retry delay"),
			max_delay_seconds:
				strategy === "none"
					? 0
					: readPositiveInteger("automationMaxDelay", "Longest retry delay"),
		},
		start_at: readTimestamp("automationStart", "Start at"),
		end_at: readTimestamp("automationEnd", "End at"),
	};
}

function setFormBusy(busy, saving = false) {
	state.formBusy = busy;
	el("scheduleForm")
		.querySelectorAll("input, select, textarea, button")
		.forEach((node) => {
			node.disabled = busy;
		});
	el("saveSchedule").textContent =
		busy && saving ? "Saving…" : "Save automation";
	el("previewSchedule").textContent =
		busy && !saving ? "Checking…" : "Preview next runs";
	el("scheduleForm").setAttribute("aria-busy", String(busy));
	if (!busy) updateFormFields();
}

async function requestPreview(request) {
	const preview = await api("/v1/schedules/preview", {
		method: "POST",
		body: JSON.stringify(request),
	});
	if (preview.validation_errors?.length)
		throw new Error(preview.validation_errors.join("; "));
	const formatter = new Intl.DateTimeFormat(undefined, {
		dateStyle: "medium",
		timeStyle: "long",
		timeZone: request.timezone,
	});
	el("schedulePreview").innerHTML =
		`<h3>Next runs · ${escapeHTML(request.timezone)}</h3>${preview.next_runs?.length ? `<ol>${preview.next_runs.map((value) => `<li>${escapeHTML(formatter.format(new Date(value)))}</li>`).join("")}</ol>` : `<p>No future occurrences within this automation's active dates.</p>`}${request.enabled ? "" : `<p class="muted">Saving paused. These are the times it would run when enabled.</p>`}`;
	return preview;
}

async function previewScheduleForm() {
	if (state.formBusy || !el("scheduleForm").reportValidity()) return;
	el("scheduleFormError").textContent = "";
	try {
		const request = readScheduleForm();
		setFormBusy(true);
		await requestPreview(request);
	} catch (error) {
		el("scheduleFormError").textContent = error.message;
	} finally {
		setFormBusy(false);
	}
}

async function saveScheduleForm(event) {
	event.preventDefault();
	if (state.formBusy) return;
	el("scheduleFormError").textContent = "";
	let saved = false;
	try {
		const request = readScheduleForm();
		setFormBusy(true, true);
		await requestPreview(request);
		const id = state.editingSchedule?.id;
		const schedule = await api(
			id ? `/v1/schedules/${encodeURIComponent(id)}` : "/v1/schedules",
			{ method: id ? "PUT" : "POST", body: JSON.stringify(request) },
		);
		saved = true;
		el("scheduleDialog").close();
		showToast(
			`Automation saved ${schedule.enabled ? "and enabled" : "paused"}`,
		);
		setView("schedules");
		await refreshAll();
	} catch (error) {
		if (saved) showToast(`Saved. Refresh failed: ${error.message}`);
		else el("scheduleFormError").textContent = error.message;
	} finally {
		setFormBusy(false);
	}
}

function closeScheduleForm() {
	if (!state.formBusy) el("scheduleDialog").close();
}

function setView(view) {
	state.view = view;
	document.querySelectorAll(".tab").forEach((item) => {
		const active = item.dataset.view === view;
		item.classList.toggle("active", active);
		item.setAttribute("aria-current", active ? "page" : "false");
	});
	document.querySelectorAll(".view").forEach((node) => {
		node.classList.toggle("active", node.id === `${view}View`);
	});
}

async function refreshAll() {
	try {
		await loadStatus();
		await loadSchedules();
		await loadRuns({ reset: true });
		await loadTemplates();
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
	el("createSchedule").addEventListener("click", () => openScheduleForm());
	el("closeScheduleForm").addEventListener("click", closeScheduleForm);
	el("cancelScheduleForm").addEventListener("click", closeScheduleForm);
	el("previewSchedule").addEventListener("click", previewScheduleForm);
	el("scheduleForm").addEventListener("submit", saveScheduleForm);
	el("scheduleForm").addEventListener("input", invalidatePreview);
	el("scheduleDialog").addEventListener("cancel", (event) => {
		if (state.formBusy) event.preventDefault();
	});
	document.addEventListener("keydown", (event) => {
		if (!el("drawer").classList.contains("open")) return;
		if (event.key === "Escape") closeDrawer();
		if (event.key !== "Tab") return;
		const nodes = [
			...el("drawer").querySelectorAll(
				"button, a[href], input, select, textarea, summary, [tabindex='0']",
			),
		].filter((node) => !node.disabled && node.getClientRects().length > 0);
		const first = nodes[0];
		const last = nodes[nodes.length - 1];
		if (event.shiftKey && document.activeElement === first) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && document.activeElement === last) {
			event.preventDefault();
			first.focus();
		}
	});
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
			setView(tab.dataset.view);
		});
	});
}

bind();
refreshAll();
