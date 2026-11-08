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

`shell.New[Deps](shell.Config{Dir, Env, Timeout, MaxBytes, AllowedCommands,
DeniedCommands, DeniedOperators, BlockInteractive, DeniedEnvPatterns})`
returns an ordinary `tool.Tool[Deps]` named `run_command` whose single
required argument is a `command` string. One call:

1. Validates the arguments — a missing, non-string, or empty `command`
   rejects the call with `*model.ModelRetry`, so the agent's tool
   retry budget governs correction.
2. Checks the command policy (below); a denial is also a correctable
   `*model.ModelRetry` and happens before any process starts.
3. Runs the command through the platform interpreter (`sh -c` on Unix,
   `cmd /C` on Windows) in `Dir` (default: the process working
   directory), with `Env` entries appended after the process
   environment.
4. Bounds the run: `Timeout` (default 60s) is a hard context deadline
   — an expired or canceled run fails at the tool stage with the
   context error preserved, never as a correctable rejection.
5. Returns combined stdout and stderr in write order (both share one
   pipe), capped at `MaxBytes` (default 1 MiB); dropped bytes are
   marked with a `[shell: output truncated at N bytes]` line.

A non-zero exit status is evidence, not a failure: the result ends
with a `[shell: exited with status N]` line so the model can see the
failure and correct the command within the retry budget. Failures to
start the command at all — a missing interpreter, an unreadable
directory — fail at the tool stage with the source error preserved.

## Command policy

The zero-value `Config` filters nothing. Every policy field is opt-in,
and `New` validates the shape so a misconfiguration fails at
registration, not at run time:

| Field | Effect |
|---|---|
| `AllowedCommands` | Non-empty switches to allowlist mode: only commands whose executable matches an entry run. Entries without a path separator match the executable's base name (`"ls"` matches `/bin/ls`); entries with one match the full form. Mutually exclusive with `DeniedCommands`. |
| `DeniedCommands` | Denylist with the same matching; ignored when an allowlist is set. `shell.DestructiveCommands` is the exported starting-point list (`rm`, `dd`, `shutdown`, …). |
| `DeniedOperators` | Rejects a command whose string contains any listed operator, so one call cannot chain or redirect. `shell.ChainingOperators` is the starting-point list (`;`, `|`, `&`, `>`, `<`, `` ` ``, `$(`). Matching is a conservative substring check: quoting that mentions an operator in an argument is denied too. |
| `BlockInteractive` | Blocks executables that ordinarily expect a terminal (editors, pagers, `ssh`, `sudo`) so a call cannot hang until the timeout. |
| `DeniedEnvPatterns` | Drops environment entries whose KEY matches a glob from the merged environment — the process environment plus `Env` — so credentials in either place stay out of spawned commands. `shell.CredentialEnvPatterns` is the exported starting-point list (`*_API_KEY`, `*_TOKEN`, …). |

Every denial is a `*model.ModelRetry`: the run continues, the denial
reason reaches the model, and the correction consumes the tool retry
budget set with `golem.WithToolRetries` (zero by default — a run with
no retry budget treats the first denial as terminal). Operator checks
run before the command lists, so the model always sees the closest
correction.

## Example

Run `examples/command-execution` — offline: a scripted fake model asks
to run one denied command, receives the correctable rejection, and
corrects to an allowed one.

```go
run := shell.MustNew[struct{}](shell.Config{
	Timeout:           10 * time.Second,
	DeniedCommands:    shell.DestructiveCommands,
	DeniedEnvPatterns: shell.CredentialEnvPatterns,
})

agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithTools[struct{}, string](run),
	golem.WithToolRetries[struct{}, string](3),
)
```

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
- `shell.Config{Dir string, Env []string, Timeout time.Duration, MaxBytes int64, AllowedCommands, DeniedCommands, DeniedOperators, DeniedEnvPatterns []string, BlockInteractive bool}`
- `shell.ToolName`, `shell.ToolDescription` — the tool's stable identity
- `shell.DefaultTimeout`, `shell.DefaultMaxBytes`
- `shell.DestructiveCommands`, `shell.ChainingOperators`, `shell.CredentialEnvPatterns` — starting-point lists for the policy fields

## Gotchas

- The command string is model-produced and executes with the process's
  privileges. This tool is strictly opt-in; treat registering it as a
  security decision, not a default.
- The command policy is a guardrail against accidents, not a security
  boundary: the executable check inspects only the command's first
  token, and allowlisted commands (`python`, `git`, `make`) can spawn
  arbitrary processes. For untrusted work, run commands under OS-level
  isolation of your choosing; no configuration inside this package
  replaces that.
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
