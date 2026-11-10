# Plan Runner

Hand a build plan for a git repository to an agent on the build server. Agents submit it over
MCP; one job at a time, the runner clones the repo into a fresh workspace, has the agent carry out
the plan (building, testing and committing per phase), then pushes the commits and keeps a log. The
page lists jobs with each one's plan, commits and live log.

One image, run as separate instances that share nothing:

| Instance | Agent | For | Port |
| --- | --- | --- | --- |
| `plan-runner` (this stack) | Claude Code | personal projects | 8129 on the PC |
| `plan-runner-work` (you set it up) | Codex CLI, your OpenAI API key | work | your choice |

## MCP

`http://<pc>:8129/mcp` with `Authorization: Bearer <token>`:

| Tool | |
| --- | --- |
| `submit_plan` | `repo` (https git URL), `plan` (Markdown), optional `branch`, `title`, `push` (default true). Returns the job id. |
| `get_job` | status (queued, running, done, failed, cancelled), step, commits, the last log lines |
| `get_log` | more of the log, by line |
| `list_jobs`, `cancel_job` | |

The page is at `http://<pc>:8129`; open `/?key=<token>` once per browser (as with Ask Me).

## How a job runs

1. **Clone:** `git clone --single-branch` (the token is added for `https://github.com/...` URLs)
   into `/data/jobs/<id>/workspace`. The plan is saved next to it as `plan.md`.
2. **Run** the executor in the workspace, with a time limit (`TIME_LIMIT_MINUTES`, default 120):
   - `claude`: `claude -p <prompt> --permission-mode bypassPermissions`, plus Ask Me as an MCP
     server when `ASKME_URL` is set
   - `codex`: `codex exec --dangerously-bypass-approvals-and-sandbox <prompt>`
   - `command`: `sh -c "$EXECUTOR_COMMAND"`, with `PLAN_FILE`, `WORKSPACE`, `BRANCH` and `PROMPT`
     in the environment
   The prompt tells the agent to follow the repo's own instructions, build and test as it goes,
   commit per phase, not push, and end with a summary. The container is the sandbox: no Docker
   socket and no host folders. The agent gets its model credentials but not the runner's token or
   the git token.
3. **Push:** if the executor succeeded and made commits, the runner pushes them to the same branch.
   A failed, cancelled or timed-out job is never pushed. Uncommitted changes are reported, not pushed.

## Settings

| Variable | |
| --- | --- |
| `TOKEN` | MCP, API and page (from the stack's `PLANRUNNER_TOKEN`) |
| `EXECUTOR` | `claude`, `codex` or `command` |
| `CLAUDE_CODE_OAUTH_TOKEN` | Claude Code's login, from `claude setup-token` |
| `OPENAI_API_KEY` | Codex's key (work instance) |
| `GIT_TOKEN`, `GIT_NAME`, `GIT_EMAIL` | clone/push credentials and the commit author |
| `ASKME_URL`, `ASKME_TOKEN` | optional: where the agent asks the owner (claude only) |
| `TIME_LIMIT_MINUTES` | per job, default 120 |

## The work instance

Work code must reach OpenAI only, so the work instance is kept apart from everything Claude
touches. Set it up yourself, so its secrets never pass through a Claude session:

1. Pick a host other than the PC if you can (the PC's build box holds the PC's Docker socket).
2. In Portainer, create a stack `plan-runner-work` from this compose file with: a different
   `container_name` and port, its own volume, `EXECUTOR=codex`, and in the stack's environment
   `PLANRUNNER_TOKEN` (new), `OPENAI_API_KEY`, `GIT_TOKEN` (one that can reach the work repos), and
   no `ASKME_*` and no `CLAUDE_CODE_OAUTH_TOKEN`. Use the `plan-runner:latest` image built here
   (no new build needed on the PC; elsewhere, build it from this folder).
3. Point Codex's MCP config at it: `http://<host>:<port>/mcp` with the bearer token.

## Working on it

    tools/trinket check
    tools/trinket deploy plan-runner       # builds on the build box (the PC)
    tools/trinket deploy registry          # when gadget.json changes
    go vet ./... && go test ./...          # on the build box: a stand-in agent against local git repos

The source is at `/src` in the container.
