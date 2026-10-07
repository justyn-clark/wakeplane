import { DurableObject } from "cloudflare:workers";
import worker, { Supervisor } from "./supervisor.mjs";

export class WakeplaneDaemon extends DurableObject {
	constructor(ctx, env) {
		super(ctx, env);
		this.supervisor = new Supervisor(ctx.container, ctx.storage, env);
		if (ctx.container.running) {
			void ctx.blockConcurrencyWhile(() =>
				ctx.container.setInactivityTimeout(120_000),
			);
		}
	}

	async alarm() {
		await this.ctx.blockConcurrencyWhile(() => this.supervisor.tick());
	}

	async fetch(request) {
		const path = new URL(request.url).pathname;
		try {
			if (path === "/_adapter/start" || path === "/_adapter/stop") {
				if (request.method !== "POST")
					return new Response("Method Not Allowed", { status: 405 });
				await this.ctx.blockConcurrencyWhile(() =>
					path.endsWith("/start")
						? this.supervisor.start()
						: this.supervisor.stop(),
				);
				return Response.json({ enabled: path.endsWith("/start") });
			}
			if (!(await this.ctx.storage.get("enabled"))) {
				return new Response("Activate with POST /_adapter/start", {
					status: 503,
				});
			}
			await this.ctx.blockConcurrencyWhile(() => this.supervisor.tick());
			// Startup may still be in progress. Preserve the caller's auth and
			// return a retryable 503 instead of reporting false readiness.
			return await this.ctx.container.getTcpPort(8080).fetch(request);
		} catch {
			return new Response("Daemon unavailable", { status: 503 });
		}
	}
}

export default worker;
