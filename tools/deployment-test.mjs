import assert from "node:assert/strict";
import test from "node:test";
import worker, { Supervisor } from "../infra/cloudflare/supervisor.mjs";

function fixture() {
	const values = new Map();
	const calls = [];
	const container = {
		running: false,
		images: { daemon: "digest-pinned-image" },
		start(options) {
			calls.push(options);
			this.running = true;
		},
		async setInactivityTimeout(ms) {
			calls.push(ms);
		},
		signal(signal) {
			calls.push(signal);
		},
		async monitor() {
			this.running = false;
		},
	};
	const storage = {
		async get(key) {
			return values.get(key);
		},
		async put(key, value) {
			values.set(key, value);
		},
		async setAlarm(value) {
			values.set("alarm", value);
		},
		async deleteAlarm() {
			values.delete("alarm");
		},
	};
	const env = {
		WAKEPLANE_AUTH_TOKEN: "test-only",
		WAKEPLANE_DATABASE_URL: "postgres://fixture",
	};
	return {
		values,
		calls,
		container,
		storage,
		env,
		supervisor: new Supervisor(container, storage, env, () => 1000),
	};
}

test("disabled adapter performs no startup or scheduling", async () => {
	const f = fixture();
	await f.supervisor.tick();
	assert.deepEqual(f.calls, []);
	assert.equal(f.values.get("alarm"), undefined);
});

test("activation establishes durable watchdog and uses external Postgres", async () => {
	const f = fixture();
	await f.supervisor.start();
	assert.equal(f.values.get("enabled"), true);
	assert.equal(f.values.get("alarm"), 31_000);
	assert.equal(f.calls[0].env.WAKEPLANE_STORE, "postgres");
	assert.equal(f.calls[0].image, "digest-pinned-image");
	await f.supervisor.tick();
	assert.equal(f.calls.filter((call) => typeof call === "object").length, 1);
});

test("host restart is recovered without a client request", async () => {
	const f = fixture();
	await f.supervisor.start();
	f.container.running = false;
	const recovered = new Supervisor(f.container, f.storage, f.env, () => 2000);
	await recovered.tick();
	assert.equal(f.calls.filter((call) => typeof call === "object").length, 2);
	assert.equal(f.values.get("alarm"), 32_000);
});

test("startup failures leave a durable retry alarm without leaking credentials", async () => {
	const f = fixture();
	f.container.start = () => {
		throw new Error("fixture-start-failure");
	};
	await assert.rejects(f.supervisor.start(), /fixture-start-failure/);
	assert.equal(f.values.get("alarm"), 31_000);
	f.container.start = () => {
		f.container.running = true;
	};
	await f.supervisor.tick();
	assert.equal(f.container.running, true);
});

test("missing secrets fail closed without starting an ephemeral SQLite ledger", async () => {
	const f = fixture();
	delete f.env.WAKEPLANE_DATABASE_URL;
	await assert.rejects(f.supervisor.start(), /required/);
	assert.deepEqual(f.calls, []);
});

test("deactivation survives a Durable Object restart", async () => {
	const f = fixture();
	await f.supervisor.start();
	await f.supervisor.stop();
	assert.equal(f.values.get("alarm"), undefined);
	assert.ok(f.calls.includes(15));
	const count = f.calls.length;
	await new Supervisor(f.container, f.storage, f.env).tick();
	assert.equal(f.calls.length, count);
});

test("unauthorized requests cannot activate, stop or route to the singleton", async () => {
	for (const token of [undefined, "wrong", "test-only-suffix"]) {
		const f = fixture();
		f.env.DAEMON = {
			get() {
				assert.fail("must not access daemon");
			},
		};
		const request = new Request("https://fixture/_adapter/start", {
			method: "POST",
			headers: token ? { Authorization: `Bearer ${token}` } : {},
		});
		assert.equal((await worker.fetch(request, f.env)).status, 401);
	}
});

test("authorized routing always selects the same daemon identity", async () => {
	const f = fixture();
	f.env.DAEMON = {
		idFromName(name) {
			assert.equal(name, "wakeplane-singleton");
			return "one";
		},
		get(id) {
			assert.equal(id, "one");
			return { fetch: async () => new Response("ok") };
		},
	};
	const request = new Request("https://fixture/v1/status", {
		headers: { Authorization: "Bearer test-only" },
	});
	assert.equal((await worker.fetch(request, f.env)).status, 200);
});
