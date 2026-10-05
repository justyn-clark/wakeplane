import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const source = await readFile(
	new URL("../internal/api/console/app.js", import.meta.url),
	"utf8",
);

function harness(values = {}, editingSchedule = null, documentExtras = {}) {
	const fields = new Map();
	for (const [id, value] of Object.entries(values)) {
		fields.set(id, typeof value === "boolean" ? { checked: value } : { value });
	}
	const context = vm.createContext({
		Intl,
		URL,
		localStorage: { getItem: () => "" },
		document: { getElementById: (id) => fields.get(id), ...documentExtras },
	});
	vm.runInContext(
		`${source.replace(/\nbind\(\);[\s\S]*$/, "")}\n globalThis.helpers = { state, readScheduleForm, readJSONField, readTimestamp, safeURL, externalJobPanel, cronPreset, canReconcileExternalJob, syncTaskFieldsFromJSON, syncJSONFromTaskFields, updateCachedRun, findDrawerReturnFocus };`,
		context,
	);
	context.helpers.state.editingSchedule = editingSchedule;
	return { ...context.helpers, fields };
}

function validFields() {
	return {
		automationName: "Weekly summary",
		automationEnabled: false,
		automationTimezone: "America/Los_Angeles",
		automationKind: "cron",
		automationFrequency: "weekly",
		automationClock: "09:30",
		automationWeekday: "1",
		automationTarget: "http",
		automationMethod: "POST",
		automationURL: "https://runner.example/jobs",
		automationBody: '{"task":"weekly-summary"}',
		automationRepository: "acme/project",
		automationFeeds: "https://feed.example/rss",
		automationNotify: "",
		automationHeaders: "{}",
		automationTrackJob: true,
		automationPoll: "5",
		automationLookup: "https://runner.example/jobs/lookup",
		automationOverlap: "forbid",
		automationMisfire: "run_once_if_late",
		automationTimeout: "300",
		automationConcurrency: "1",
		automationRetry: "none",
		automationStart: "",
		automationEnd: "",
	};
}

test("friendly weekly timing produces explicit timezone and a paused tracked job", () => {
	const { readScheduleForm } = harness(validFields());
	const request = readScheduleForm();
	assert.equal(request.timezone, "America/Los_Angeles");
	assert.equal(request.enabled, false);
	assert.equal(request.schedule.expr, "30 9 * * 1");
	assert.equal(request.target.http_job.poll_interval_seconds, 5);
	assert.equal(
		request.target.http_job.lookup_url,
		"https://runner.example/jobs/lookup",
	);
	assert.equal(request.retry.strategy, "none");
	assert.equal(request.retry.max_attempts, 1);
});

test("inspecting completed work refreshes the ledger without losing summary labels", () => {
	const { state, updateCachedRun } = harness();
	state.runs = [
		{
			id: "run_1",
			status: "pending",
			schedule_name: "Repository watch",
			target_kind: "http",
		},
		{
			id: "run_2",
			status: "pending",
			schedule_name: "Reading list",
			target_kind: "http",
		},
	];
	assert.equal(
		updateCachedRun({
			id: "run_1",
			status: "succeeded",
			finished_at: "2026-10-05T20:00:00Z",
			external_job: { status: "succeeded" },
		}),
		true,
	);
	assert.equal(state.runs[0].status, "succeeded");
	assert.equal(state.runs[0].external_job.status, "succeeded");
	assert.equal(state.runs[0].schedule_name, "Repository watch");
	assert.equal(state.runs[0].target_kind, "http");
	assert.equal(state.runs[1].status, "pending");
	assert.equal(
		updateCachedRun({ id: "run_absent", status: "succeeded" }),
		false,
	);
	assert.equal(state.runs.length, 2);
	assert.equal(
		updateCachedRun({ id: "run_1", status: "succeeded" }, "pending"),
		true,
	);
	assert.equal(state.runs.length, 1);
	assert.equal(state.runs[0].id, "run_2");
});

test("drawer return focus follows the visible mobile card after resizing or removing its opener", () => {
	const control = (
		runID,
		visible = true,
		connected = true,
		disabled = false,
	) => ({
		isConnected: connected,
		disabled,
		dataset: { openRun: runID },
		getClientRects: () => (visible ? [{}] : []),
		closest: (selector) =>
			selector === "[data-open-run]" && runID
				? { dataset: { openRun: runID } }
				: null,
	});
	const desktop = control("run_1", false);
	const mobile = control("run_1");
	const filter = control(null);
	const tab = control(null);
	const { fields, findDrawerReturnFocus } = harness({}, null, {
		querySelectorAll: () => [desktop, mobile],
		querySelector: () => tab,
	});
	fields.set("statusFilter", filter);
	assert.equal(findDrawerReturnFocus(desktop), mobile);
	assert.equal(findDrawerReturnFocus(control("run_1", false, false)), mobile);
	assert.equal(findDrawerReturnFocus(mobile), mobile);
	mobile.disabled = true;
	assert.equal(findDrawerReturnFocus(desktop), filter);
	filter.disabled = true;
	assert.equal(findDrawerReturnFocus(desktop), tab);
});

test("guided task inputs preserve custom settings and synchronize advanced JSON", () => {
	const {
		readScheduleForm,
		fields,
		syncJSONFromTaskFields,
		syncTaskFieldsFromJSON,
	} = harness({
		...validFields(),
		automationBody: '{"task":"weekly-summary","custom":{"tag":"go"}}',
		automationFeeds: "https://feed.example/rss\n\nhttps://feed.example/atom",
		automationNotify: "https://notify.example/hook",
	});
	syncJSONFromTaskFields();
	const body = readScheduleForm().target.body;
	assert.equal(body.feeds.length, 2);
	assert.equal(body.custom.tag, "go");
	assert.equal(body.notify_url, "https://notify.example/hook");
	fields.get("automationBody").value =
		'{"task":"repository-watch","repository":"acme/another","custom":true}';
	syncTaskFieldsFromJSON();
	assert.equal(fields.get("automationRepository").value, "acme/another");
	assert.equal(readScheduleForm().target.body.custom, true);
});

test("guided feeds, repository names, and notification receivers reject invalid values", () => {
	for (const value of [
		"",
		"file:///etc/passwd",
		"https://user:secret@feed.example/rss",
		Array(6).fill("https://feed.example/rss").join("\n"),
	]) {
		const { readScheduleForm } = harness({
			...validFields(),
			automationFeeds: value,
		});
		assert.throws(readScheduleForm, /feed|Feed/);
	}
	const repository = harness({
		...validFields(),
		automationBody: '{"task":"repository-watch","keep":true}',
		automationRepository: "https://github.com/acme/project",
	});
	assert.throws(repository.readScheduleForm, /owner\/name/);
	repository.fields.get("automationRepository").value = " acme/project ";
	assert.equal(
		repository.readScheduleForm().target.body.repository,
		"acme/project",
	);
	assert.equal(repository.readScheduleForm().target.body.keep, true);
	const notification = harness({
		...validFields(),
		automationNotify: "javascript:alert(1)",
	});
	assert.throws(notification.readScheduleForm, /Notification receiver/);
});

test("editing intervals preserves anchor, date bounds, and additional target options", () => {
	const values = {
		...validFields(),
		automationKind: "interval",
		automationInterval: "60",
		automationAnchor: "2026-10-05T09:00:00-07:00",
		automationStart: "2026-10-05T09:00:00-07:00",
		automationEnd: "2026-10-06T09:00:00-07:00",
		automationTrackJob: false,
	};
	const original = {
		target: {
			kind: "http",
			credentials: { runner: "env:RUNNER_TOKEN" },
			http_job: { poll_interval_seconds: 10 },
		},
	};
	const { readScheduleForm } = harness(values, original);
	const request = readScheduleForm();
	assert.equal(request.schedule.anchor_at, values.automationAnchor);
	assert.equal(request.start_at, values.automationStart);
	assert.equal(request.end_at, values.automationEnd);
	assert.equal(request.target.credentials.runner, "env:RUNNER_TOKEN");
	assert.equal(request.target.http_job, undefined);
	assert.equal(request.schedule.expr, undefined);
});

test("changing target kind does not leak prior headers or job options", () => {
	const values = {
		...validFields(),
		automationTarget: "shell",
		automationCommand: "/usr/local/bin/backup",
		automationArgs: '["--destination","/backups"]',
	};
	const { readScheduleForm } = harness(values, {
		target: {
			kind: "http",
			headers: { Authorization: "Bearer secret" },
			http_job: {},
		},
	});
	const request = readScheduleForm();
	assert.equal(request.target.command, "/usr/local/bin/backup");
	assert.equal(request.target.args.length, 2);
	assert.equal(request.target.headers, undefined);
	assert.equal(request.target.http_job, undefined);
});

test("malformed instruction objects, headers, argument arrays, and numeric settings are rejected", () => {
	for (const body of ["[]", "null", '"text"', "{"]) {
		const { readScheduleForm } = harness({
			...validFields(),
			automationBody: body,
		});
		assert.throws(readScheduleForm, /Instructions/);
	}
	const badHeaders = harness({
		...validFields(),
		automationHeaders: '{"X-Count":2}',
	});
	assert.throws(badHeaders.readScheduleForm, /Header values must be text/);
	const badArgs = harness({
		...validFields(),
		automationTarget: "shell",
		automationCommand: "backup",
		automationArgs: "[2]",
	});
	assert.throws(badArgs.readScheduleForm, /array of text values/);
	for (const value of ["0", "-1", "1.5", "NaN", "9007199254740992"]) {
		const { readScheduleForm } = harness({
			...validFields(),
			automationTimeout: value,
		});
		assert.throws(readScheduleForm, /positive whole number/);
	}
});

test("one-time schedules require an explicit offset and omit previous recurrence fields", () => {
	const { readScheduleForm, fields } = harness({
		...validFields(),
		automationKind: "once",
		automationOnce: "2026-10-06T09:00:00",
	});
	assert.throws(readScheduleForm, /explicit offset/);
	fields.get("automationOnce").value = "2026-10-06T09:00:00-07:00";
	const request = readScheduleForm();
	assert.equal(request.schedule.at, "2026-10-06T09:00:00-07:00");
	assert.equal(request.schedule.expr, undefined);
});

test("runner artifact links reject executable schemes and escape supplied labels", () => {
	const { safeURL, externalJobPanel } = harness();
	assert.equal(safeURL("javascript:alert(1)"), null);
	assert.equal(safeURL("data:text/html,hello"), null);
	assert.equal(safeURL("/relative"), null);
	const panel = externalJobPanel({
		status: "succeeded",
		job_id: "test-job",
		progress: { percent: 100, message: "<script>alert(1)</script>" },
		artifacts: [
			{ name: "<img onerror=alert(1)>", url: "https://runner.example/report" },
			{ name: "dangerous", url: "javascript:alert(1)" },
		],
	});
	assert.match(panel, /&lt;script&gt;/);
	assert.match(panel, /&lt;img onerror=alert\(1\)&gt;/);
	assert.match(panel, /rel="noopener noreferrer"/);
	assert.doesNotMatch(panel, /javascript:/);
	assert.doesNotMatch(panel, /<script>/);
});

test("custom cron expressions are preserved and ordinary recurrences are recognized", () => {
	const { cronPreset } = harness();
	assert.equal(cronPreset("30 9 * * 1").frequency, "weekly");
	assert.equal(cronPreset("30 9 * * 1-5").frequency, "weekdays");
	assert.equal(cronPreset("*/5 * * * *").frequency, "custom");
	const { readScheduleForm } = harness({
		...validFields(),
		automationFrequency: "custom",
		automationCron: "*/5 * * * *",
	});
	assert.equal(readScheduleForm().schedule.expr, "*/5 * * * *");
});

test("report outcomes show readable source items and honest notification failures safely", () => {
	const { externalJobPanel } = harness();
	const panel = externalJobPanel({
		status: "succeeded",
		result: {
			title: "Weekly reading summary",
			summary: "Source excerpts and links",
			items: [
				null,
				{
					title: "<script>Title</script>",
					url: "javascript:alert(1)",
					source: "Feed",
					excerpt: "<img src=x onerror=alert(1)>",
				},
			],
			delivery: {
				status: "failed",
				attempts: 3,
				last_error: "Notification endpoint returned HTTP 502",
			},
		},
	});
	assert.match(panel, /Weekly reading summary/);
	assert.match(panel, /Notification/);
	assert.match(panel, /3 delivery attempts/);
	assert.match(panel, /HTTP 502/);
	assert.match(panel, /&lt;script&gt;Title/);
	assert.doesNotMatch(panel, /<img/);
	assert.doesNotMatch(panel, /href="javascript:/);
});

test("runner status recovery is available only for unconfirmed terminal local runs", () => {
	const { canReconcileExternalJob } = harness();
	const job = { job_id: "remote-1", status: "running" };
	assert.equal(
		canReconcileExternalJob({ status: "dead_lettered", external_job: job }),
		true,
	);
	for (const status of ["pending", "claimed", "running", "retry_scheduled"])
		assert.equal(canReconcileExternalJob({ status, external_job: job }), false);
	assert.equal(
		canReconcileExternalJob({
			status: "failed",
			external_job: { ...job, status: "succeeded" },
		}),
		false,
	);
	assert.equal(
		canReconcileExternalJob({
			status: "failed",
			external_job: { ...job, compacted: true },
		}),
		false,
	);
	assert.equal(
		canReconcileExternalJob({
			status: "failed",
			external_job: { ...job, job_id: "" },
		}),
		false,
	);
	assert.equal(
		canReconcileExternalJob({
			status: "failed",
			external_job: { status: "submitting", can_reconcile: true },
		}),
		true,
	);
	assert.equal(
		canReconcileExternalJob({
			status: "failed",
			external_job: { ...job, can_reconcile: false },
		}),
		false,
	);
});
