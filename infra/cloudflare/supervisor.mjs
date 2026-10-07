// Cloudflare owns container lifecycle; Wakeplane retains scheduling and policy.
export class Supervisor {
	constructor(container, storage, env, now = Date.now) {
		this.container = container;
		this.storage = storage;
		this.env = env;
		this.now = now;
		this.starting = undefined;
	}

	async start() {
		await this.storage.put("enabled", true);
		await this.tick();
	}

	async stop() {
		await this.storage.put("enabled", false);
		await this.storage.deleteAlarm();
		if (this.container.running) {
			this.container.signal(15);
			await this.container.monitor();
		}
	}

	async tick() {
		if (!(await this.storage.get("enabled"))) return;
		// Persist the next wake before attempting startup, including on failure.
		await this.storage.setAlarm(this.now() + 30_000);
		if (this.starting) return this.starting;
		this.starting = this.ensureRunning();
		try {
			await this.starting;
		} finally {
			this.starting = undefined;
		}
	}

	async ensureRunning() {
		if (!this.env.WAKEPLANE_AUTH_TOKEN || !this.env.WAKEPLANE_DATABASE_URL) {
			throw new Error("Daemon token and external Postgres URL are required");
		}
		if (!this.container.running) {
			this.container.start({
				image: this.container.images.daemon,
				enableInternet: true,
				instance: "lite",
				env: {
					PORT: "8080",
					WAKEPLANE_STORE: "postgres",
					WAKEPLANE_DATABASE_URL: this.env.WAKEPLANE_DATABASE_URL,
					WAKEPLANE_AUTH_TOKEN: this.env.WAKEPLANE_AUTH_TOKEN,
					WAKEPLANE_WORKER_ID: "cloudflare-singleton",
				},
			});
		}
		await this.container.setInactivityTimeout(120_000);
	}
}

export function authorized(request, env) {
	return (
		Boolean(env.WAKEPLANE_AUTH_TOKEN) &&
		request.headers.get("Authorization") ===
			`Bearer ${env.WAKEPLANE_AUTH_TOKEN}`
	);
}

export default {
	async fetch(request, env) {
		if (!authorized(request, env))
			return new Response("Unauthorized", { status: 401 });
		const object = env.DAEMON.get(env.DAEMON.idFromName("wakeplane-singleton"));
		return object.fetch(request);
	},
};
