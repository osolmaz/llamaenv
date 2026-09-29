---
date: 2026-09-29
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: llamaenv diagnostics
tags: [llamaenv, diagnostics, logs]
---

# llamaenv diagnostics

## Why

On 2026-09-29 a Prism runtime wedged on a Windows laptop after a client
aborted a stream and sent it again. The router answered `/health` and
`/models`, but chat requests hung, the GPU sat at 0%, and killing the model's
child did not recover it: the router kept the dead child `loaded`. There were
no logs to read, so the cause could not be found.

### A possible cause: output backpressure

The switcher copied each runtime router's output, line by line, into its own
stderr, which is a pipe that the Llama app reads. The llama.cpp logger blocks
when its queue is full (`common/log.cpp`, `cv_full.wait`). So if that pipe
stops draining:

1. the switcher's copy blocks, and the router's stdout pipe fills;
2. every `LOG` call in the router blocks, so a chat request stops at
   `proxying request`, while `/health` and `/models`, which log nothing, still
   answer;
3. the router's thread that forwards the child's output blocks, so the child's
   logger blocks inside inference and the GPU goes idle;
4. that thread never sees the child exit, so the router keeps it `loaded`.

This matches every symptom, and the switcher's own messages had the same
problem: `makeActive` logged to stderr while it held the lock that every model
request takes. llamaenv must not block on output in any case.

It is not confirmed as the cause. On the laptop, with the switcher's output
going into a pipe that nobody read, the old build wedged once in 4 rounds of
abort and resend, then recovered, which a full pipe does not explain. In a
later control run of 8 rounds under the same conditions it did not wedge. The
new build passed 8 of 8. The live wedge stays unexplained. These diagnostics
exist so that the next one can be read.

### What the laptop tests ruled out

About 40 rounds of abort and resend, with a 24K-token prompt, from the laptop,
over the tailnet, and from inside a Docker container:

- The switcher passes client aborts on. It reads the whole request body, so
  `net/http` sees the client go away, and `httputil.ReverseProxy` cancels the
  router request. With the new event log, each aborted request ended within
  about 6 seconds of the abort.
- Prism's router and child cancel an aborted prefill from a clean state, and
  recover when the child is killed, idle or while streaming.

### A bug found with the new event log

The first laptop run showed aborted requests that never ended in the event
log, each with a false stall a minute later. When a client goes away
mid-response, `httputil.ReverseProxy` panics with `http.ErrAbortHandler`,
which `net/http` expects, and the code after the proxy call did not run. The
end of a request is now recorded in a `defer`.

## What upstream provides

Checked: `common/log.cpp` and `common/arg.cpp` in `PrismML-Eng/llama.cpp`
`prism-b10743-adfffbe` and `ggml-org/llama.cpp`, and
`LlamaApp.LlamaCpp/LlamaManager.cs` in `ggml-org/Llama-Windows`.

- llama.cpp has `--log-file`. The file has no size limit, and the logger still
  writes to the console, so it does not remove the backpressure. llamaenv does
  not add it: the client's arguments reach every router unchanged.
- The Windows Llama app drains the server's stdout and stderr, at Debug level,
  only for a server that it started itself.

llamaenv owns the runtime routers that it starts, so their output is its
concern. The default router keeps the client's standard streams, as with the
official `llama serve` (principle 3).

## Design

Diagnostics are on by default, have no settings, and use a fixed disk budget.
They live in llamaenv's data folder, `logs/<port>/`, one folder per switcher
(principle 10), and uninstall removes them (principle 8).

- **Nothing blocks on output.** A runtime router's output goes to a file, then
  to the switcher's stderr through a bounded queue that drops lines when the
  reader does not keep up. The switcher's own messages take the same path.
  Dropped lines are counted in the event log.
- **Runtime output:** `<runtime>.log`, up to 8 MB, then moved to
  `<runtime>.log.1`, which replaces the older one. Each start also moves the
  previous run's file to `.1`. Lines are cut at 4 KB.
- **Events:** `events.jsonl`, one JSON object per line, with the same limits.
  It records the switcher's start, its messages, runtime exits, and each
  request that runs a model: model, runtime, status, bytes, time to first
  byte, duration, and whether the client went away first.
- **Stall snapshot:** when a model request gets no bytes for 60 seconds, the
  switcher records, once for that request, the runtime router's `/health`, the
  model's `/models` entry, whether the model's child port accepts connections,
  and `/slots`. Each check has a short timeout, so a wedged router shows up as
  a timeout. It also writes the switcher's goroutine stacks to
  `stall-<time>.txt`, and keeps the three newest.
- **Commands:** `llamaenv status` also lists the running switcher's requests
  in flight, from its event log. `llamaenv logs` prints the log folders and
  the end of each file.

The budget per switcher is about 2 × 8 MB per runtime, plus 2 × 8 MB of
events, plus three stack dumps.

## Out of scope

A hang inside llama.cpp itself needs a process dump. llamaenv does not write
one, because a dump holds the model's memory. Use
`procdump -ma <pid>` from Sysinternals.
