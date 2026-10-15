# Parked indexing benchmark

Benchmark: cold index, two copied real parts (one complete, one interrupted), all camera files and logs.
Environment: provided ARM64 comma device, go1.27.1, fixed eight-core budget; 94 trials per build; one warmup excluded.

| Metric |     Before |      After |
| ------ | ---------: | ---------: |
| Mean   | 2903.26 ms |  984.65 ms |
| Median | 2903.07 ms |  982.47 ms |
| Min    | 2832.77 ms |  943.19 ms |
| Max    | 2978.40 ms | 1051.46 ms |
| Stdev  |   30.03 ms |   23.16 ms |
| p95    | 2954.23 ms | 1025.95 ms |
| p99    | 2978.40 ms | 1051.46 ms |

Speedup: 2.95x; mean time reduced 66.1%.
Welch t=490.424, df=174.72, p=3.4986e-276; 95% CI for improvement [1910.89, 1926.33] ms. High confidence (df >= 29).

These are repeated warm-filesystem-cache measurements of a small real sample, not a full-library rebuild or cold-boot guarantee. Memory policy uses two index workers, four media decoder threads each, one shared heavy job, a 96 MiB Go heap target and a 4 MiB SQLite page cache; no system memory probes were used.

## Changes

- Run at most two indexing workers inside one shared heavy-work slot; split eight media decoder threads between them. Yield the slot between batches so foreground work can proceed.
- Read cached summaries during inventory refresh instead of parsing full GPS, speed and engagement arrays.
- Use container timestamps for TS durations. For familiar native raw HEVC, stream Annex B NAL headers and count first-slice picture boundaries, matching FFmpeg’s [HEVC frame-boundary parser](https://github.com/FFmpeg/FFmpeg/blob/master/libavcodec/hevc/parser.c). Fall back to FFprobe for unfamiliar layouts. This counts packets; it does not claim that corrupt frames decode.
- Keep SQLite WAL with `synchronous=FULL`, a bounded page cache and incremental checkpoints. Commit each part independently; retain valid WAL files for SQLite recovery after restart.
- Write and sync thumbnails to temporary files, rename and sync their directory, then publish their index row. Retry only unfinished or changed parts after interruption.

## Reproduce on a build host

Provide disposable cache storage and recording copies. Driving-state files must explicitly confirm parked state; omit device mode on a host.

```sh
cd backend
DASH_INDEX_BENCH_ROOT=/absolute/path/to/recording-copies \
DASH_INDEX_BENCH_HOME=/absolute/path/to/disposable-benchmark-cache \
DASH_INDEX_BENCH_PARAMS=/absolute/path/to/test-params \
go test -run '^TestIndexDeviceBenchmark$' -count=1 -v -timeout=6m
```

The benchmark takes one warmup and chooses 3–100 trials within an approximately five-minute budget. Set `DASH_INDEX_BENCH_TRIALS` to the baseline count for comparison. It deletes only numbered disposable trial caches beneath its supplied home. Its JSON results and optional metadata validation output contain private recording details; do not commit them. The source recording directory is read-only.

The automated recovery tests kill a synthetic writer after uncommitted pages reach the WAL, verify committed rows and database integrity after reopening, and ensure cached parts are not rebuilt. They also cover canceled active encoders, unknown/onroad lockout and interrupted thumbnail cleanup. Process-kill tests do not simulate storage hardware that ignores flushes, or an actual vehicle power cut.
