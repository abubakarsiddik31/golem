# Benchmarks

## Purpose

Record what the hot paths cost, so a change that regresses them is
visible in review instead of discovered in production. Three suites are
measured: the agent runner loop, PDF extraction, and Office document
extraction. Each lives next to the code it measures, in a
`*_bench_test.go` file.

These are not a CI gate. Absolute numbers depend on the machine, the Go
toolchain, and the filesystem cache, so a threshold would be flaky. The
gate is the reviewer comparing `benchstat` output before and after a
change that touches one of these paths.

## Running them

```bash
scripts/bench.sh                  # all three suites, three runs each
scripts/bench.sh -benchtime=2s    # extra go test flags are forwarded
```

Then compare two runs:

```bash
scripts/bench.sh > before.txt
# ... make the change ...
scripts/bench.sh > after.txt
benchstat before.txt after.txt    # go install golang.org/x/perf/cmd/benchstat@latest
```

Every benchmark calls `b.ReportAllocs()`, so `-benchmem` columns are
always populated. The extraction benchmarks call `b.SetBytes`, so the
`MB/s` column is input-file throughput, not output throughput.

## Baseline

Measured on 2026-10-25 with `go1.26.5 darwin/arm64` on an Apple M4,
`-count 3` in one session, so every figure below is the median of three
runs. Treat the shape of the numbers as the baseline — relative costs
between benchmarks — rather than the absolute values. Run-to-run
variance is roughly 1–3% for the microbenchmarks and a little more for
the extraction suites.

### Runner loop

`internal/runner/runner_bench_test.go`

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `ExecuteSingleTurn` | 260 | 928 | 5 |
| `ExecuteToolRound` | 647 | 2,608 | 15 |
| `ExecuteLongHistory` (201 messages) | 5,992 | 115,088 | 5 |
| `RepairHistory` (2 unanswered calls, 1 orphan) | 282 | 1,408 | 5 |
| `HistoryToJSON` (101 messages) | 9,776 | 4,890 | 2 |

### PDF extraction

`pdfextract/pdf_bench_test.go`. The fixture page has a heading, two
text columns, and a ruled table; the multi-page case repeats it twenty
times.

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `ExtractSinglePage` | 44,719 | 26.1 | 59,119 | 989 |
| `ExtractMultiPage` (20 pages) | 881,271 | 21.9 | 1,191,042 | 18,935 |
| `ParsePDF` (5 pages, parser only) | 8,901 | 559.3 | 10,704 | 278 |

### Document extraction

`docextract/doc_bench_test.go`. Fixtures are built in-process by the
existing test builders, so the suites stay offline and need no
`testdata/`.

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `ExtractDocx` | 39,267 | 35.9 | 43,359 | 679 |
| `ExtractXlsx` | 45,299 | 38.5 | 59,101 | 616 |
| `ExtractPptx` | 55,521 | 40.8 | 64,709 | 666 |
| `ExtractDocxOutline` | 39,646 | 35.5 | 43,351 | 679 |

## Reading the numbers

- **The runner loop is not the bottleneck.** One model turn costs about
  260 ns of local work. A provider call is measured in hundreds of
  milliseconds, so loop overhead is roughly six orders of magnitude
  smaller. Optimizing it is not worth the complexity; keeping it
  predictable is.
- **A tool round costs about 2.5× a plain turn** (647 vs 260 ns) and
  allocates 2.8× as much, because it builds the tool result message and
  re-normalizes the history for the second model turn.
- **History cost is linear and paid per run.** `ExecuteLongHistory` at
  201 messages allocates 115 KB, almost all of it the single copy of the
  message slice that makes the loop's history immutable to the caller.
  That is deliberate: a run must never mutate the history it was given.
  The cost is O(history) per run and negligible beside a model call.
- **PDF parsing is not the cost.** `ParsePDF` takes about 1.8 µs per
  page (8,901 ns for five), while end-to-end extraction takes about
  44 µs per page (881,271 ns for twenty), so layout analysis, table
  reconstruction, and Markdown rendering account for roughly 96% of the
  time. A regression in `ExtractSinglePage` that does not appear in
  `ParsePDF` is in the layout half.
- **PDF per-page time is flat across page count** (44.7 µs for one page
  versus 44.1 µs per page for twenty), which confirms per-page work
  holds no cross-page state. If the multi-page time per page rises
  relative to the single-page one, something is accumulating.
  The `MB/s` column still differs between the two (26.1 versus 21.9)
  because the single-page fixture carries proportionally more structural
  bytes per page — 1,167 bytes per page against 965 — so the same
  per-page work moves more input bytes. Compare ns/op, not MB/s, when
  the page count changes.
- **Outline mode is not cheaper than full extraction** on a single small
  document (39,646 vs 39,267 ns, a difference inside the noise): the
  archive is inflated and the XML is parsed either way, so outline mode
  saves only output rendering. It is a filtering feature, not a fast
  path. Expect the gap to widen only on documents large enough for
  body-text rendering to matter.

## Adding a benchmark

Put it in the package's existing `*_bench_test.go`, reuse the test
fixtures rather than adding `testdata/`, and always call
`b.ReportAllocs()`. Prefer `b.Loop()` over `for i := 0; i < b.N; i++`:
it keeps setup out of the measured region and prevents the compiler from
optimizing the body away. If the benchmark reads a file, call
`b.SetBytes` so throughput is reported.

Benchmarks never run under `go test ./...`, so they do not affect
`scripts/coverage.sh`.
