# Logstreamer

Interactive CLI tool that runs several shell-backed processes at once (plus optional integrations) and **merges stdout/stderr into a single colored terminal stream**, so you can watch many log sources side by side.

## Requirements

- [Go](https://go.dev/dl/) **1.26.1** or newer (see `go.mod`)
- For **`/docker`**: Docker CLI installed and able to run `docker logs -f …`
- For **`/supabase`**: Network access and a Supabase account with a suitable **personal access token** (see below)

## Build

```bash
go build -o logstreamer .
```

## Run

```bash
./logstreamer
# or during development:
go run .
```

## Usage

On startup you are asked **how many commands** you want from the outset. Enter a non‑negative integer, then paste one shell command per line (each runs as `sh -c '…'`). Pipelines and redirects work like in your shell.

Example initial commands:

- `nginx -g 'daemon off;'` alongside `tail -f /var/log/app.log`
- `npm run dev` next to another service

After startup you get a **`logstreamer>`** prompt. Control verbs:

| Command     | Meaning |
|------------|---------|
| `/add`     | Prompt for another command and attach its logs |
| `/docker`  | Prompt for a **container ID** and stream `docker logs -f …` via the shell backend |
| `/supabase`| Prompt for project **URL or ref** and a **personal access token**; polls the [Management API logs endpoint](https://supabase.com/docs/reference/api/v1-get-project-logs) in the background |
| `/quit`    | Shut down cleanly (signals children, drains streamer workers) |

Empty lines at the prompt are ignored. Sending **EOF** on stdin (e.g. **Ctrl+D** on some setups) behaves like quitting with a clean shutdown.

Press **Ctrl+C** or deliver **SIGTERM** to interrupt; the handler cancels integrations, stops subprocesses with **SIGTERM**, then exits.

## Supabase credentials

`/supabase` uses the Supabase **Management API** (`https://api.supabase.com`). You need:

1. **Project ref** — either paste the subdomain ref (e.g. `abcdefghijklmnop`) or the full project URL (`https://abcdefghijklmnop.supabase.co`).
2. **Personal access token** — created under your Supabase **Account → Access Tokens**, with **`analytics_logs_read`** (or equivalent scope that allows analytics log reads) for the project.

This is **not** the project’s anon or service-role API key used by client apps.

## Project layout

| Path | Role |
|------|------|
| `main.go` | Entry — `app.Run()`, exit code to OS |
| `app/` | REPL, signal handling, wiring integrations |
| `streamer/` | Process spawning (`sh -c`), stdout/stderr fan-in, locking, shutdown |
| `ui/` | Line-oriented prompts (`bufio.Scanner` on stdin) |
| `integrations/docker/` | Wraps `docker logs -f` as a streamed command |
| `integrations/supabase/` | HTTPS polling + deduped print path into the streamer |

## Exit codes

- **0** — normal quit (`/quit`, EOF), or interrupt path (currently exits via `os.Exit(0)` after shutdown)
- **1** — input or setup errors printed to stderr during the initial questionnaire or stdin errors
