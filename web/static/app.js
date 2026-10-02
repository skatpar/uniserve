(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const money = (n) => '$' + Math.round(n || 0).toLocaleString('en-US');
  const gb = (mb) => Math.round((mb || 0) / 1024);
  const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

  let state = null;
  let view = 'norm';
  let asking = false;

  const svg = (body, size = 12) => `<svg viewBox="0 0 12 12" width="${size}" height="${size}" aria-hidden="true">${body}</svg>`;
  const ICON = {
    active: svg('<circle cx="6" cy="6" r="4" fill="currentColor"/>'),
    free: svg('<circle cx="6" cy="6" r="3.5" fill="none" stroke="currentColor" stroke-width="1.5"/>'),
    idle: svg('<rect x="2.5" y="2" width="2.5" height="8" rx="1" fill="currentColor"/><rect x="7" y="2" width="2.5" height="8" rx="1" fill="currentColor"/>'),
    fault: svg('<path d="M6 1 11.2 10.5H.8z" fill="currentColor"/><path d="M6 4.6v2.9" stroke="var(--surface)" stroke-width="1.3" stroke-linecap="round"/><circle cx="6" cy="8.9" r=".75" fill="var(--surface)"/>'),
    offline: svg('<circle cx="6" cy="6" r="4" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M3.2 8.8 8.8 3.2" stroke="currentColor" stroke-width="1.5"/>'),
    away: svg('<path d="M6 1 11 6 6 11 1 6z" fill="currentColor"/><path d="M6 3.8v2.7" stroke="var(--surface)" stroke-width="1.3" stroke-linecap="round"/><circle cx="6" cy="8.1" r=".75" fill="var(--surface)"/>'),
    lock: svg('<rect x="2.5" y="5.2" width="7" height="5.3" rx="1.2" fill="currentColor"/><path d="M4 5.2V3.8a2 2 0 0 1 4 0v1.4" fill="none" stroke="currentColor" stroke-width="1.4"/>', 11),
    check: svg('<path d="M2.2 6.3 4.9 9 9.8 3.4" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>'),
  };

  const STATE = {
    active: { label: 'Active', icon: ICON.active },
    idle: { label: 'Quiet', icon: ICON.idle },
    hoarded: { label: 'Held idle', icon: ICON.idle },
    free: { label: 'Free', icon: ICON.free },
    faulted: { label: 'Fault', icon: ICON.fault },
    offline: { label: 'Offline', icon: ICON.offline },
  };

  const vendorVar = (v) => ({ NVIDIA: 'var(--v-nvidia)', Huawei: 'var(--v-huawei)', AMD: 'var(--v-amd)' }[v] || 'var(--v-other)');

  // Only touch the DOM when the markup actually changed.
  function setHTML(el, html) {
    if (el.__html !== html) {
      el.innerHTML = html;
      el.__html = html;
    }
  }

  function human(ms) {
    const s = Math.max(0, Math.floor(ms / 1000));
    if (s >= 172800) { const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600); return h ? `${d}d ${h}h` : `${d}d`; }
    if (s >= 3600) return `${Math.floor(s / 3600)}h`;
    if (s >= 60) return `${Math.floor(s / 60)}m`;
    return `${s}s`;
  }

  function gpuLabel(id) {
    const i = id.lastIndexOf(':');
    return i < 0 ? id : `${id.slice(0, i)} GPU ${id.slice(i + 1)}`;
  }

  /* ---------- Summary tiles ---------- */

  function sparkline(hist) {
    if (!hist || hist.length < 2) return '<div class="spark-cap"><span>Collecting samples</span></div>';
    const wrap = document.querySelector('.spark-wrap');
    const W = Math.max(120, (wrap && wrap.clientWidth) || 220), H = 34, pad = 5;
    const lo = Math.max(0, Math.min(...hist) - 10), hi = Math.min(100, Math.max(...hist) + 10);
    const x = (i) => pad + (i * (W - 2 * pad)) / (hist.length - 1);
    const y = (v) => H - pad - ((v - lo) / Math.max(1, hi - lo)) * (H - 2 * pad);
    const d = hist.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join(' ');
    const last = hist[hist.length - 1];
    const span = human(hist.length * 2000);
    return `<div class="spark-wrap"><svg class="spark" viewBox="0 0 ${W} ${H}" role="img" aria-label="Fleet utilization over the last ${span}, between ${Math.round(Math.min(...hist))}% and ${Math.round(Math.max(...hist))}%">
      <title>Last ${span}: ${Math.round(Math.min(...hist))}% to ${Math.round(Math.max(...hist))}%</title>
      <path d="${d}"/><circle cx="${x(hist.length - 1).toFixed(1)}" cy="${y(last).toFixed(1)}" r="4"/></svg></div>
      <div class="spark-cap"><span>${span} ago</span><span>now</span></div>`;
  }

  function renderKPIs(s) {
    const k = s.kpis, home = s.policy.home, homeName = s.policy.home_name;
    const working = k.active + k.idle;
    const stack = k.by_vendor.map((v) => `<span style="flex:${v.gpus};background:${vendorVar(v.vendor)}" title="${esc(v.vendor)}: ${v.gpus} GPUs"></span>`).join('');
    const legend = k.by_vendor.map((v) => `<span><i style="background:${vendorVar(v.vendor)}"></i>${esc(v.vendor)} ${v.gpus}</span>`).join('');
    const top = [...k.by_vendor].sort((a, b) => b.gpus - a.gpus)[0];
    const faultFindings = s.findings.filter((f) => f.type === 'fault' && f.severity === 'critical');

    const tiles = [
      `<div class="tile">
        <div class="label">Fleet</div>
        <div class="value">${k.gpus}<small>GPUs</small></div>
        <div class="sub">${plural(k.nodes, 'node', 'nodes')}, ${plural(k.by_vendor.length, 'vendor', 'vendors')}</div>
        <div class="extra"><div class="stack">${stack}</div><div class="legend">${legend}</div></div>
      </div>`,
      `<div class="tile">
        <div class="label">Average utilization</div>
        <div class="value">${Math.round(k.avg_util)}%</div>
        <div class="sub">${working} working, ${k.hoarded} held idle, ${k.free} free</div>
        <div class="extra">${sparkline(s.history)}</div>
      </div>`,
      `<div class="tile ${k.hoarded ? 'alert-warning' : ''}">
        <div class="label">Held idle</div>
        <div class="value">${money(k.waste_per_day)}<small>/day</small></div>
        <div class="sub">${k.hoarded
          ? `${plural(k.hoarded, 'GPU', 'GPUs')} allocated but doing nothing. ${money(k.wasted_so_far)} so far.`
          : 'Nothing is being held idle.'}</div>
        <div class="extra sub">${k.hoarded ? `${k.reclaimable_gb} GB reclaimable` : `${k.free_gb} GB free`} of fleet costing ${money(k.fleet_cost_per_day)}/day</div>
      </div>`,
      `<div class="tile ${k.faulted ? 'alert-critical' : ''}">
        <div class="label">Health</div>
        <div class="value">${k.faulted ? plural(k.faulted, 'fault', 'faults') : 'Healthy'}</div>
        <div class="sub">${k.faulted
          ? (faultFindings.length ? `${plural(faultFindings.length, 'workload', 'workloads')} down: ${esc([...new Set(faultFindings.map((f) => f.workload))].join(', '))}` : 'No workload affected. Hardware awaiting repair.')
          : `All ${k.gpus - k.offline} reporting GPUs are OK.`}</div>
        <div class="extra sub">${k.offline ? `${plural(k.offline, 'GPU', 'GPUs')} on nodes that stopped reporting` : `${k.nodes_online} of ${k.nodes} nodes reporting`}</div>
      </div>`,
      `<div class="tile ${k.residency_violations ? 'alert-serious' : ''}">
        <div class="label">Sovereignty</div>
        <div class="value">${Math.round(k.in_country_pct)}%<small>in ${esc(homeName)}</small></div>
        <div class="sub">${k.residency_violations
          ? `${plural(k.residency_violations, 'restricted workload', 'restricted workloads')} running outside ${esc(home)}.`
          : `No restricted data outside ${esc(home)}.`}</div>
        <div class="extra sub">${top ? `Largest single vendor: ${esc(top.vendor)}, ${Math.round(top.pct)}% of GPUs` : ''}</div>
      </div>`,
    ];
    setHTML($('kpis'), tiles.join(''));
  }

  /* ---------- Nodes ---------- */

  function gpuTile(g, n, s) {
    const home = s.policy.home;
    const wl = g.workload;
    const violation = wl && wl.data_class === 'restricted' && n.jurisdiction !== home && g.state !== 'faulted';
    const st = STATE[g.state] || STATE.active;
    const badge = violation
      ? `<span class="badge">${ICON.away}Outside ${esc(home)}</span>`
      : `<span class="badge">${st.icon}${st.label}</span>`;
    const name = wl ? wl.name : (g.drained ? 'Drained' : g.state === 'faulted' ? 'Out of service' : 'Unallocated');
    const owner = wl
      ? `${wl.data_class === 'restricted' ? `<span class="lock" title="Restricted data">${ICON.lock}</span>` : ''}<span>${esc(wl.owner)} · ${esc(wl.data_class)}</span>`
      : '';
    const memPct = g.mem_total_mb ? Math.min(100, (g.mem_used_mb / g.mem_total_mb) * 100) : 0;
    let foot = `${Math.round(g.temp_c)}°C · ${Math.round(g.power_w)} W`;
    if (g.state === 'hoarded') {
      const idle = g.idle_since ? human(new Date(s.time) - new Date(g.idle_since)) : '';
      foot = `<b>idle ${idle}</b> · ${money(n.cost_per_gpu_hour * 24)}/day`;
    } else if (g.state === 'faulted') {
      foot = `<b>${esc(g.health_code || 'critical')}</b>`;
    } else if (g.state === 'offline') {
      foot = 'no recent report';
    }
    const tip = [gpuLabel(g.id), wl && wl.name, g.health_raw].filter(Boolean).join('\n');
    return `<div class="gpu ${esc(g.state)} ${violation ? 'violation' : ''}" title="${esc(tip)}">
      <div class="gpu-top"><span class="idx">GPU ${g.index}</span>${badge}</div>
      <div class="wl">${esc(name)}</div>
      <div class="owner">${owner}</div>
      <div class="meter"><span>Util</span><div class="track"><div class="fill" style="width:${Math.min(100, g.util_pct)}%"></div></div><span class="num">${Math.round(g.util_pct)}%</span></div>
      <div class="meter mem"><span>Mem</span><div class="track"><div class="fill" style="width:${memPct}%"></div></div><span class="num">${gb(g.mem_used_mb)}/${gb(g.mem_total_mb)} GB</span></div>
      <div class="gpu-foot">${foot}</div>
    </div>`;
  }

  function renderNodes(s) {
    const home = s.policy.home;
    const html = s.nodes.map((n) => {
      const inHome = n.jurisdiction === home;
      const jName = s.jurisdictions[n.jurisdiction] || n.jurisdiction;
      const jTag = inHome
        ? `<span class="tag home" title="${esc(jName)}">${ICON.check}${esc(n.jurisdiction)} · in-country</span>`
        : `<span class="tag foreign" title="${esc(jName)}">${ICON.away}${esc(n.jurisdiction)} · outside ${esc(home)}</span>`;
      const src = { live: '<span class="tag live" title="Reported by an agent on the node">Live</span>', replay: '<span class="tag" title="Agent is replaying captured tool output">Replay</span>' }[n.source]
        || '<span class="tag sim" title="Generated by the built-in simulator">Simulated</span>';
      const cp = s.copilot.enabled && s.copilot.host_node === n.id ? '<span class="tag" title="The copilot model is served from this node">Hosts copilot</span>' : '';
      const off = n.online ? '' : '<span class="tag foreign">Not reporting</span>';
      const body = view === 'raw'
        ? `<div class="raw-cap">What <code>${esc(n.raw_tool || 'the vendor tool')}</code> prints on this node${n.source === 'simulated' ? ' (generated by the simulator)' : ''}:</div><pre class="raw">${esc(n.raw || 'No raw output was sent.')}</pre>`
        : `<div class="gpus">${n.gpus.map((g) => gpuTile(g, n, s)).join('')}</div>`;
      return `<article class="node ${n.online ? '' : 'offline'}">
        <div class="node-head">
          <div>
            <div class="node-title"><span class="host">${esc(n.id)}</span><span class="hw"><i class="vdot" style="background:${vendorVar(n.vendor)}"></i>${esc(n.vendor)} · ${n.gpus.length} × ${esc(n.accelerator)}</span></div>
            <div class="node-meta"><span>${esc(n.provider || 'Unknown provider')}</span>${n.site ? `<span class="dotsep">${esc(n.site)}</span>` : ''}${n.cost_per_gpu_hour ? `<span class="dotsep">$${n.cost_per_gpu_hour.toFixed(2)} per GPU-hour</span>` : ''}</div>
          </div>
          <div class="node-tags">${off}${cp}${jTag}${src}</div>
        </div>
        ${body}
      </article>`;
    }).join('');
    setHTML($('nodes'), html || '<div class="card empty">No nodes are reporting yet. Start an agent, or run the server with the simulator on.</div>');
  }

  /* ---------- Findings and activity ---------- */

  function explainQuestion(f) {
    if (f.type === 'fault') return `Explain the fault on ${gpuLabel(f.gpu)}: what does it mean, what is affected, and what should we do?`;
    if (f.type === 'residency') return `Explain the residency problem with ${f.workload} and how to fix it.`;
    if (f.type === 'hoarded') return `Should we reclaim ${gpuLabel(f.gpu)}? Who holds it and what is it costing?`;
    return `What should we do about ${f.node} not reporting?`;
  }

  function renderFindings(s) {
    const fs = s.findings;
    setHTML($('findings-count'), fs.length ? plural(fs.length, 'item', 'items') : '');
    if (!fs.length) {
      setHTML($('findings'), `<div class="empty">${ICON.check}Nothing needs attention.</div>`);
      return;
    }
    const icon = { fault: ICON.fault, residency: ICON.away, hoarded: ICON.idle, offline: ICON.offline };
    setHTML($('findings'), fs.map((f) => {
      const waste = f.waste_per_day ? ` <span class="money">${money(f.waste_per_day)}/day</span>, ${money(f.wasted_so_far)} so far.` : '';
      const notes = f.plan && f.plan.notes && f.plan.notes.length ? esc(f.plan.notes.join(' ')) : '';
      const plan = f.plan ? `<div class="f-plan"><span class="lbl">Proposed:</span> ${esc(f.plan.summary)}.${notes ? ` <span class="f-notes">${notes}</span>` : ''}</div>` : '';
      const approve = f.plan
        ? `<button class="btn small primary" data-approve="${esc(f.id)}" ${f.plan.executable ? '' : 'title="Agents are read-only: this must be done on the node"'}>${f.plan.executable ? 'Approve' : 'How to apply'}</button>`
        : '';
      return `<div class="finding ${esc(f.severity)}">
        <div class="f-title">${icon[f.type] || ''}<span>${esc(f.title)}</span></div>
        <div class="f-detail">${esc(f.detail)}${waste}</div>
        ${plan}
        <div class="f-actions">${approve}<button class="btn small" data-explain="${esc(f.id)}">Ask copilot</button></div>
      </div>`;
    }).join(''));
  }

  function renderEvents(s) {
    const now = new Date(s.time);
    const rows = s.events.slice(0, 8).map((e) => {
      const age = now - new Date(e.time);
      return `<li class="${esc(e.kind)}"><time>${age < 4000 ? 'now' : human(age) + ' ago'}</time><span>${esc(e.text)}</span></li>`;
    }).join('');
    setHTML($('events'), rows || '<li><time></time><span>No activity yet.</span></li>');
  }

  /* ---------- Copilot ---------- */

  const PRESETS = [
    ['Do we need more GPUs?', 'Do we need to buy more GPUs?'],
    ['Explain the fault', 'Explain the current fault: what does the vendor code mean, what is affected, and what should we do?'],
    ['Place a restricted 40 GB model', 'Where can I run a 40 GB model on restricted banking data?'],
    ['What if NVIDIA is cut off?', 'What breaks if NVIDIA GPUs become unavailable?'],
  ];

  function renderModelLine(s) {
    const c = s.copilot, el = $('model-line');
    if (!c.enabled) {
      el.className = 'model-line';
      setHTML(el, '<span class="dot"></span><span>No model connected. Answers come from built-in rules and say so.</span>');
      return;
    }
    const where = {
      local: 'running on this machine. Fleet telemetry stays here.',
      private: 'on your private network. Fleet telemetry stays inside it.',
      external: 'a public endpoint. Fleet telemetry leaves your network with every question.',
      unknown: 'location could not be determined.',
    }[c.locality] || '';
    const host = c.host_node ? ` Served from <b>${esc(c.host_node)}</b>, a node in this fleet.` : '';
    el.className = 'model-line ' + (c.locality === 'external' ? 'ext' : 'on');
    setHTML(el, `<span class="dot"></span><span><b>${esc(c.model || 'default model')}</b> at ${esc(c.endpoint)}, ${where}${host}</span>`);
  }

  function md(src) {
    const inline = (t) => t
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>')
      .replace(/(^|[\s(])\*([^*\s][^*]*)\*(?=[\s).,;:!?]|$)/g, '$1<i>$2</i>');
    let html = '', list = null, para = [];
    const flushP = () => { if (para.length) { html += '<p>' + inline(para.join(' ')) + '</p>'; para = []; } };
    const closeL = () => { if (list) { html += `</${list}>`; list = null; } };
    for (const raw of esc(src).split('\n')) {
      const l = raw.trim();
      let m;
      if (!l) { flushP(); closeL(); continue; }
      if ((m = l.match(/^[-*•]\s+(.*)/))) { flushP(); if (list !== 'ul') { closeL(); html += '<ul>'; list = 'ul'; } html += '<li>' + inline(m[1]) + '</li>'; continue; }
      if ((m = l.match(/^\d+[.)]\s+(.*)/))) { flushP(); if (list !== 'ol') { closeL(); html += '<ol>'; list = 'ol'; } html += '<li>' + inline(m[1]) + '</li>'; continue; }
      if ((m = l.match(/^#{1,6}\s+(.*)/))) { flushP(); closeL(); html += '<p><b>' + inline(m[1]) + '</b></p>'; continue; }
      closeL(); para.push(l);
    }
    flushP(); closeL();
    return html;
  }

  async function ask(question) {
    question = (question || '').trim();
    if (!question || asking) return;
    asking = true;
    $('ask-btn').disabled = true;
    const box = $('answer');
    box.hidden = false;
    box.innerHTML = `<div class="q">${esc(question)}</div><div class="by" id="ans-by"></div><div class="note" id="ans-note" hidden></div><div class="body" id="ans-body"><span class="thinking">Reading the fleet</span></div>`;
    // The copilot sits at the top of the side panel. Bring it into view
    // without moving the fleet the operator is looking at.
    if (matchMedia('(max-width: 1180px)').matches) box.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    else document.querySelector('.side').scrollTo({ top: 0, behavior: 'smooth' });
    try {
      const res = await fetch('api/v1/copilot', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ question }) });
      if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || res.statusText);
      const source = res.headers.get('X-Answer-Source'), model = res.headers.get('X-Answer-Model'), note = res.headers.get('X-Answer-Note');
      $('ans-by').innerHTML = source === 'model'
        ? `Answered by <span class="src">${esc(model || 'model')}</span> from the live fleet snapshot`
        : 'Answered by <span class="src">built-in rules</span> (not a language model)';
      if (note) { $('ans-note').hidden = false; $('ans-note').textContent = note + ' Fell back to rules.'; }
      const reader = res.body.getReader(), dec = new TextDecoder();
      let text = '';
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        text += dec.decode(value, { stream: true });
        $('ans-body').innerHTML = md(text);
      }
      if (!text.trim()) $('ans-body').textContent = 'No answer came back.';
    } catch (err) {
      $('ans-body').textContent = 'Could not get an answer: ' + err.message;
    } finally {
      asking = false;
      $('ask-btn').disabled = false;
    }
  }

  /* ---------- Actions ---------- */

  let toastTimer;
  function toast(msg) {
    const t = $('toast');
    t.textContent = msg;
    t.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { t.hidden = true; }, 6000);
  }

  async function post(path, body) {
    const res = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}) });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  async function approve(id) {
    try {
      const out = await post('api/v1/actions', { finding_id: id });
      toast('Done: ' + out.done + '.');
    } catch (err) {
      toast(err.message);
    }
    refresh();
  }

  /* ---------- Wiring ---------- */

  function renderChrome(s) {
    setHTML($('home-chip'), `Home jurisdiction: <b>${esc(s.policy.home_name)} (${esc(s.policy.home)})</b>`);
    const sims = s.nodes.filter((n) => n.source === 'simulated').length;
    setHTML($('foot'), `UniServe ${esc(s.version)}. ${sims ? `${plural(sims, 'node is', 'nodes are')} simulated and marked as such. ` : ''}A GPU counts as held idle after ${esc(s.idle_after)} allocated with under 5% utilization. Money figures use the hourly rate configured per node.`);
    const menu = $('demo-menu');
    if (s.sim.enabled && !menu.__built) {
      menu.__built = true;
      menu.hidden = false;
      $('demo-list').innerHTML = '<div class="menu-label">Inject a fault</div>' +
        s.sim.scenarios.map((sc) => `<button role="menuitem" data-fault="${esc(sc.id)}">${esc(sc.label)}</button>`).join('') +
        '<hr><button role="menuitem" data-reset="1">Reset the simulated fleet</button>';
    }
  }

  async function refresh() {
    try {
      const res = await fetch('api/v1/state', { cache: 'no-store' });
      state = await res.json();
    } catch (err) {
      return;
    }
    renderChrome(state);
    renderKPIs(state);
    renderNodes(state);
    renderFindings(state);
    renderEvents(state);
    renderModelLine(state);
  }

  function setView(v) {
    view = v;
    $('view-norm').classList.toggle('on', v === 'norm');
    $('view-raw').classList.toggle('on', v === 'raw');
    $('view-norm').setAttribute('aria-pressed', v === 'norm');
    $('view-raw').setAttribute('aria-pressed', v === 'raw');
    if (state) renderNodes(state);
  }

  function initTheme() {
    let saved = null;
    try { saved = localStorage.getItem('uniserve-theme'); } catch (e) { /* storage unavailable */ }
    if (saved) document.documentElement.dataset.theme = saved;
    $('theme-btn').addEventListener('click', () => {
      const cur = document.documentElement.dataset.theme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
      const next = cur === 'dark' ? 'light' : 'dark';
      document.documentElement.dataset.theme = next;
      try { localStorage.setItem('uniserve-theme', next); } catch (e) { /* storage unavailable */ }
    });
  }

  function initDialog() {
    const dlg = $('llm-dialog'), msg = $('llm-msg');
    $('llm-settings-btn').addEventListener('click', () => {
      const c = (state && state.copilot) || {};
      $('llm-url').value = c.base_url || '';
      $('llm-model').value = c.model || '';
      $('llm-key').value = '';
      $('llm-key').placeholder = c.has_key ? 'A key is saved. Leave empty to keep it.' : 'Not needed for most self-hosted endpoints';
      $('llm-node').innerHTML = '<option value="">Not specified</option>' + ((state && state.nodes) || []).map((n) => `<option value="${esc(n.id)}" ${c.host_node === n.id ? 'selected' : ''}>${esc(n.id)}</option>`).join('');
      msg.textContent = ''; msg.className = 'dialog-msg';
      dlg.showModal();
    });
    $('llm-cancel').addEventListener('click', () => dlg.close());
    $('llm-disconnect').addEventListener('click', async () => {
      await post('api/v1/settings/llm', { base_url: '' }).catch(() => {});
      dlg.close();
      toast('Model disconnected. The copilot now answers from built-in rules.');
      refresh();
    });
    $('llm-form').addEventListener('submit', async (e) => {
      e.preventDefault();
      msg.className = 'dialog-msg'; msg.textContent = 'Testing the endpoint';
      try {
        const out = await post('api/v1/settings/llm', {
          base_url: $('llm-url').value, model: $('llm-model').value, api_key: $('llm-key').value, host_node: $('llm-node').value,
        });
        if (out.ok) {
          dlg.close();
          toast(`Copilot connected to ${out.status.model || 'the endpoint'} at ${out.status.endpoint}.`);
        } else {
          msg.className = 'dialog-msg err';
          msg.textContent = 'Saved, but the endpoint did not answer: ' + out.error;
        }
      } catch (err) {
        msg.className = 'dialog-msg err'; msg.textContent = err.message;
      }
      refresh();
    });
  }

  function init() {
    initTheme();
    initDialog();
    $('presets').innerHTML = PRESETS.map(([label, q]) => `<button class="chip-btn" type="button" data-q="${esc(q)}" title="${esc(q)}">${esc(label)}</button>`).join('');
    $('presets').addEventListener('click', (e) => { const b = e.target.closest('.chip-btn'); if (b) ask(b.dataset.q); });
    $('ask-form').addEventListener('submit', (e) => { e.preventDefault(); const v = $('ask-input').value; $('ask-input').value = ''; ask(v); });
    $('view-norm').addEventListener('click', () => setView('norm'));
    $('view-raw').addEventListener('click', () => setView('raw'));

    $('findings').addEventListener('click', (e) => {
      const a = e.target.closest('[data-approve]'), x = e.target.closest('[data-explain]');
      if (a) approve(a.dataset.approve);
      if (x && state) { const f = state.findings.find((q) => q.id === x.dataset.explain); if (f) ask(explainQuestion(f)); }
    });

    const btn = $('demo-btn'), list = $('demo-list');
    const closeMenu = () => { list.hidden = true; btn.setAttribute('aria-expanded', 'false'); };
    btn.addEventListener('click', (e) => { e.stopPropagation(); list.hidden = !list.hidden; btn.setAttribute('aria-expanded', String(!list.hidden)); });
    document.addEventListener('click', closeMenu);
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeMenu(); });
    list.addEventListener('click', async (e) => {
      const f = e.target.closest('[data-fault]'), r = e.target.closest('[data-reset]');
      try {
        if (f) { const out = await post('api/v1/sim/fault', { scenario: f.dataset.fault }); toast('Fault injected on ' + gpuLabel(out.gpu) + '.'); }
        if (r) { await post('api/v1/sim/reset'); $('answer').hidden = true; toast('Simulated fleet reset.'); }
      } catch (err) { toast(err.message); }
      refresh();
    });

    refresh();
    setInterval(refresh, 2000);
  }

  init();
})();
