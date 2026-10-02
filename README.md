# UniServe

One view and one copilot for a mixed-vendor GPU fleet. UniServe reads NVIDIA
and Huawei Ascend accelerators through their own tools, puts them in one
schema, finds wasted capacity, faults and data-residency problems, and lets
you ask about all of it in plain language using a model you host yourself.

It is a single binary with no dependencies: control plane, web console,
simulator and node agent.

## Run it

    ./uniserve server

Open http://localhost:8080. A simulated fleet of four nodes (NVIDIA H200,
Huawei Ascend 910B3, AMD MI300X, and a rented NVIDIA node abroad) starts with
it, so every feature can be shown without hardware.

Build from source with Go 1.22 or newer: `go build -o uniserve .`

## Connect the copilot to a model

Any OpenAI-compatible endpoint works (vLLM, vLLM-Ascend, LM Studio, Ollama):

    ./uniserve server \
      --llm-url http://10.0.0.7:8000/v1 \
      --llm-model qwen3-32b \
      --llm-node khi-ascend-01      # optional: the fleet node serving the model

or set it at runtime under "Model settings" in the console. The console shows
whether the endpoint is on this machine, on a private network, or a public
address, because fleet telemetry is sent with every question.

With no model configured, or if the model cannot be reached, a small rules
engine answers the core questions instead. Every answer is labelled with its
source, so rules output is never presented as model output.

## Add a real node

Copy the binary for the node's architecture to the node and run:

    ./uniserve agent --server http://<control-plane>:8080 \
      --node gpu-box-01 --provider "On-prem" --site Islamabad \
      --jurisdiction PK --cost 3.40

The agent finds `nvidia-smi` or `npu-smi` on its own, reads them every five
seconds and pushes the result over an outbound HTTP connection. It opens no
ports and runs nothing it is sent: it is read-only by design. Set `--token` on
both server and agents to require a shared secret.

No live access during a demo? Capture the tool output once and replay it:

    npu-smi info > capture.txt
    ./uniserve agent --server http://localhost:8080 --node ascend-01 \
      --replay capture.txt --jurisdiction PK --cost 1.50

`examples/` has one capture for each vendor. Replayed nodes are tagged
"Replay" in the console.

## What it does

| Area | What you get |
|---|---|
| One schema | NVIDIA and Ascend telemetry normalized: memory, utilization, temperature, power, health, processes. A toggle shows the raw vendor output next to it. |
| Idle capacity | GPUs that are allocated but quiet beyond a threshold are flagged with owner, duration and cost per day. |
| Faults | Vendor health codes (NVIDIA Xid, Ascend health alarms) raised as findings with the affected workload and a proposed move. |
| Sovereignty | Every node has a jurisdiction. Restricted workloads outside the home jurisdiction are flagged. What-if analysis shows what survives losing a vendor or all foreign capacity. |
| Copilot | Answers from the live snapshot plus the engine's computed findings. The engine does the arithmetic; the model explains it. |
| Human approval | Every proposed action needs a click. On simulated nodes the action is applied; on real nodes the console says what to do, because agents are read-only. |

## How it fits together

    nvidia-smi / npu-smi
            |  (agent: parse, normalize, push outbound)
            v
    POST /api/v1/ingest ----> store ----> engine ----> console
                                 ^           |
                    simulator ---+           +----> copilot ----> your model endpoint

    internal/collect   vendor parsers (add a vendor here, nothing else changes)
    internal/agent     node-side loop
    internal/store     in-memory fleet state, idle tracking, activity log
    internal/engine    KPIs, findings, action plans, what-if, placement
    internal/copilot   prompt building, streaming, rules fallback
    internal/sim       simulated fleet and fault scenarios
    internal/server    HTTP API
    web/static         console (embedded in the binary)

## Server flags

| Flag | Default | Meaning |
|---|---|---|
| `--addr` | `:8080` | Listen address |
| `--home` | `PK` | Home jurisdiction; restricted data must stay here |
| `--sim` | `true` | Run the simulated fleet (`--sim=false` for real nodes only) |
| `--idle-after` | `10m` | Quiet time before an allocated GPU counts as held idle |
| `--offline-after` | `20s` | Silence before a node counts as offline |
| `--token` | none | Shared secret agents must present |
| `--llm-url`, `--llm-model`, `--llm-key`, `--llm-node` | none | Copilot endpoint |

Each has an environment variable: `UNISERVE_ADDR`, `UNISERVE_HOME`,
`UNISERVE_TOKEN`, `UNISERVE_LLM_URL`, `UNISERVE_LLM_MODEL`,
`UNISERVE_LLM_KEY`, `UNISERVE_LLM_NODE`.

## What is real and what is not

- Real: the NVIDIA and Ascend parsers, the agent, ingest, idle detection, the
  findings engine, what-if and placement, the copilot and its rules fallback.
- Simulated: the four default nodes, their workloads, owners, data classes and
  hourly rates, the raw tool output shown for them, and the injected faults.
  They are tagged "Simulated" in the console.
- AMD appears only as a simulated node. There is no AMD collector yet.
- The Ascend parser is tested against a sample of `npu-smi info` output for
  910B cards, not yet against live hardware. Check it on a real node first.
- On real nodes, workload name and owner come from the process holding the
  most memory and the user who started it. There is no scheduler integration.
- State is in memory. Restarting the server clears history.
- The console has no login. Run it on a trusted network.

## Tests

    go test ./...
