# Production visuals and examples

The [production case study](public/in-production.md) covers hosted reports on Railway and local jobs on a Mac Mini. Its evidence was captured October 26, 2026; these files are a dated record, not a live status dashboard.

## Visuals

All assets live in [media/production-2026-10/](media/production-2026-10/). Screenshots and video use actual UI, cropped and masked for privacy. Open an image for its full resolution.

| Asset                                                                      | What it shows                                                                       |
| -------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| [System design](media/production-2026-10/architecture.png)                 | Hosted and local execution/storage boundaries                                       |
| [Railway deployment](media/production-2026-10/railway.png)                 | Daemon, runner, Postgres and persistent volumes                                     |
| [Discord delivery](media/production-2026-10/discord.png)                   | An actual scheduled repository report                                               |
| [Local run ledger](media/production-2026-10/ledger.png)                    | Recorded outcomes and attempt counts; internal identifiers are masked               |
| [Local schedules](media/production-2026-10/schedules.png)                  | Jobs, timing and scheduling policies                                                |
| [Walkthrough video](media/production-2026-10/walkthrough.mp4)              | Read-only navigation through existing console, infrastructure and delivery evidence |
| [Video poster](media/production-2026-10/poster.png)                        | Still image for the walkthrough                                                     |
| [Case study PDF](media/production-2026-10/case-study.pdf)                  | Three-page portable account of the setup and its limits                             |
| [Pi Durable Android preview](media/production-2026-10/android-preview.png) | Emulator fixture; not physical-phone acceptance or a live production schedule       |

Pi Durable Android and native desktop integrations remain private previews. The case study records the separate companion-read verification and outstanding desktop configuration. No jobs were triggered for the walkthrough, and no application UI was fabricated.

## Run your own

Start with the [recipe setup and delivery guide](public/recipes.md). It explains runner state, provider configuration and how to inspect results. The notification manifests start paused; configure your own timezone and provider credentials before enabling them.

| Example                         | Repository file                                                                                |
| ------------------------------- | ---------------------------------------------------------------------------------------------- |
| Repository activity             | [developer-repository-watch.yaml](../examples/developer-repository-watch.yaml)                 |
| Repository activity to Discord  | [developer-repository-watch-discord.yaml](../examples/developer-repository-watch-discord.yaml) |
| Weekly reading list             | [personal-weekly-summary.yaml](../examples/personal-weekly-summary.yaml)                       |
| Weekly reading list to Gmail    | [personal-weekly-summary-email.yaml](../examples/personal-weekly-summary-email.yaml)           |
| Separate report/delivery runner | [automation-runner/](../examples/automation-runner/)                                           |

The Mac Mini's article and docs-drift scripts are private operator workloads. The case study documents their use; those scripts and their private inputs are not included as public runnable examples. For a local shell target, see [backup-shell.yaml](../examples/backup-shell.yaml). For deployment options, see [hosting](public/hosting.md) and the [deployment adapters](../infra/README.md).
