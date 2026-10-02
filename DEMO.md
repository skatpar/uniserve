# Five-minute demo

Start with `./uniserve server` and open http://localhost:8080. If a model is
available, connect it under "Model settings" first.

## Opening line

"Sovereign AI means running on the chips you are allowed to buy, in the
country your data must stay in. That gives you a mixed fleet: NVIDIA here,
Huawei Ascend there, something rented abroad. Every vendor ships its own tool
and none of them shows you the others. UniServe is one view and one copilot
across all of it, and the copilot runs on a model the fleet itself hosts."

## 1. Two dashboards become one (45 seconds)

1. Click **Raw vendor output**. Scroll past the three tools: different
   columns, different units, different words for the same thing.
2. Click **UniServe view**. Same machines, one schema.
3. Point at the summary row: 18 GPUs, three vendors, one utilization number.

## 2. The hoarded GPU (90 seconds)

1. Point at the amber tiles on `isb-h200-01`: two H200s holding memory at 0%
   for three days.
2. Point at the **Held idle** tile: the cost per day.
3. Say: "The data science team has just asked us to buy two more H200s."
4. Click **Do we need more GPUs?** The copilot says to reclaim, names the
   GPUs, owners and money.
5. Click **Approve** on one of them. The tile goes free and the daily waste
   drops.

## 3. The 3 a.m. fault (90 seconds)

1. **Demo controls**, then **NVIDIA: GPU falls off the bus (Xid 79)**.
2. The tile turns red, Health shows one fault, and a finding appears with the
   workload that is down: the fraud scoring model, on restricted data.
3. Click **Ask copilot** on the finding. It explains Xid 79 in plain language,
   says what is affected, and proposes restarting the replica on an in-country
   GPU: the one you reclaimed a minute ago, or another idle-held one.
4. Click **Approve**. The workload restarts on the new GPU; the faulted one is
   marked drained. The activity log records who approved what.

For the Ascend version, inject **Ascend: uncorrectable HBM memory error**
instead. Same flow, different vendor's vocabulary, same plain-language answer.

## 4. Sovereignty (60 seconds)

1. Point at `fra-cloud-01`: tagged "outside PK", with a restricted KYC
   workload on it. The **Sovereignty** tile counts it as a violation.
2. Click **What if NVIDIA is cut off?** The copilot lists which workloads
   move where, which need a build for another vendor, and whether anything is
   left without capacity.
3. Optional: **Place a restricted 40 GB model**. Foreign GPUs are excluded and
   the answer says so.

## Closing line

"Three things no vendor tool gives you: every accelerator in one schema,
idle capacity priced in money, and an operations copilot that never sends
your fleet's telemetry outside your own network."

## If something goes wrong

- Model slow or down: answers fall back to built-in rules and are labelled as
  such. The demo still works; say that the fallback is deliberate.
- Fleet in a strange state: **Demo controls**, then **Reset the simulated
  fleet**.
- No access to a real node: replay a capture with `--replay` (see README).

## Likely questions

- "Is this real or a mock-up?" The simulator pushes through the same ingest
  path as the agent. Show a replayed or live node next to the simulated ones.
- "Does the agent change anything on my servers?" No. It only reads, and only
  makes outbound connections. Actions on real nodes are shown as instructions.
- "Why not Run:ai or the vendor's own tools?" They cover their own hardware.
  Run:ai's support matrix is NVIDIA data-centre GPUs only.
- "Does the model do the maths?" No. The engine computes findings and what-if
  results exactly; the model explains them. That is why a small local model
  is enough.
