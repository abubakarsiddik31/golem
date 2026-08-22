# Command execution

## Purpose

Let a model run commands: the `shell` package builds a tool that
executes one shell command and returns its combined stdout and stderr
— the third common tool, completing the fetch/read/run trio.

## When to use

Coding agents that run builds, tests, and scripts; data agents that
run local tools. Only where executing model-written commands is
acceptable in the first place: the command runs with the registering
process's privileges, and no configuration inside this package changes
that — scope what a command can touch with the working directory, the
environment, the timeout, and OS-level isolation of your choosing, or
do not register the tool. When the application already knows which
command to run, run it yourself and hand the model the result.

## How it works

`shell.New[Deps](shell.Config{Dir, Env, Timeout, MaxBytes})` returns
an ordinary `tool.Tool[Deps]` named `run_command` whose single required
argument is a `command` string. One call:

1. Validates the arguments — a missing, non-string, or empty `command`
   rejects the call with `*model.ModelRetry`, so the agent's tool
   retry budget governs correction.
2. Runs the command through the platform interpreter (`sh -c` on Unix,
   `cmd /C` on Windows) in `Dir` (default: the process working
   directory), with `Env` entries appended after the process
   environment.
3. Bounds the run: `Timeout` (default 60s) is a hard context deadline
   — an expired or canceled run fails at the tool stage with the
   context error preserved, never as a correctable rejection.
4. Returns combined stdout and stderr in write order (both share one
   pipe), capped at `MaxBytes` (default 1 MiB); dropped bytes are
   marked with a `[shell: output truncated at N bytes]` line.

A non-zero exit status is evidence, not a failure: the result ends
with a `[shell: exited with status N]` line so the model can see the
failure and correct the command within the retry budget. Failures to
start the command at all — a missing interpreter, an unreadable
directory — fail at the tool stage with the source error preserved.

## Example

Run `examples/command-execution` — offline: a scripted fake model asks
to run one local command.

```go
run := shell.MustNew[struct{}](shell.Config{Timeout: 10 * time.Second})

agent, err := golem.New[struct{}, string](client, decoder,
    golem.WithTools[struct{}, string](run))
```

## API surface

- `shell.New[Deps](shell.Config) (tool.Tool[Deps], error)` / `shell.MustNew[Deps](shell.Config) tool.Tool[Deps]`
- `shell.Config{Dir string, Env []string, Timeout time.Duration, MaxBytes int64}`
- `shell.ToolName`, `shell.ToolDescription` — the tool's stable identity
- `shell.DefaultTimeout`, `shell.DefaultMaxBytes`

## Gotchas

- The command string is model-produced and executes with the process's
  privileges. This tool is strictly opt-in; treat registering it as a
  security decision, not a default.
- `Timeout` always applies — zero selects the 60-second default rather
  than no bound; set it explicitly when commands may legitimately run
  longer.
- Output is capped in memory as the command runs, so a runaway command
  streaming gigabytes cannot exhaust memory — it hits the byte cap and
  the timeout.
- The tool's name, description, and argument schema are public
  contract; models and prompts depend on them staying stable.
- The package depends only on the standard library, `model`, and
  `tool` — never on the root package or a provider.
- Where common tools live was decided in
  `docs/adr/0015-common-tools-package.md`.
