/* Cycle canvas (ADR-0134). A typed graph editor over the cycle draft JSON.
   It is deliberately not a programming language: the palette is the closed
   block registry, edges are typed, and the server validates every save. */
(function () {
  "use strict";

  const shell = document.getElementById("canvas-shell");
  if (!shell) return;
  const projectID = shell.dataset.projectId;
  const cycleID = shell.dataset.cycleId;
  const svg = document.getElementById("canvas-svg");
  const world = document.getElementById("canvas-world");
  const stage = document.getElementById("canvas-stage");
  const statusEl = document.getElementById("canvas-status");
  const palette = document.getElementById("canvas-palette-list");
  const inspectorEmpty = document.getElementById("canvas-inspector-empty");
  const inspectorBody = document.getElementById("canvas-inspector-body");
  const inspTitle = document.getElementById("insp-title");
  const inspInstructions = document.getElementById("insp-instructions");
  const inspQueries = document.getElementById("insp-queries");
  const inspAcceptance = document.getElementById("insp-acceptance");
  const inspMeta = document.getElementById("insp-meta");
  const connectBox = document.getElementById("insp-connect-box");
  const connectTarget = document.getElementById("insp-connect-target");

  const NS = "http://www.w3.org/2000/svg";
  let graph = { blocks: [], edges: [] };
  let registry = [];
  let editable = shell.dataset.canEdit === "true";
  let selected = null; // {type:"node"|"edge", id}
  let positions = {};
  let history = [];
  let view = { x: 40, y: 20, k: 1 };

  const byKind = () => {
    const m = {};
    registry.forEach((s) => { m[s.kind] = s; });
    return m;
  };

  function setStatus(msg, cls) {
    statusEl.textContent = msg || "";
    statusEl.className = "canvas-status" + (cls ? " " + cls : "");
  }

  function snapshot() {
    return JSON.stringify({ blocks: graph.blocks, edges: graph.edges, positions: positions });
  }

  function pushHistory() {
    history.push(snapshot());
    if (history.length > 50) history.shift();
    const undo = document.getElementById("canvas-undo");
    if (undo) undo.disabled = history.length === 0;
  }

  function restore(raw) {
    const v = JSON.parse(raw);
    graph.blocks = v.blocks || [];
    graph.edges = v.edges || [];
    positions = v.positions || {};
    selected = null;
    render();
  }

  function layout() {
    const incoming = {};
    const adj = {};
    graph.blocks.forEach((b) => { incoming[b.id] = []; adj[b.id] = []; });
    graph.edges.forEach((e) => {
      if (incoming[e.to] && adj[e.from]) {
        incoming[e.to].push(e.from);
        adj[e.from].push(e.to);
      }
    });
    const layer = {};
    const queue = graph.blocks.filter((b) => incoming[b.id].length === 0).map((b) => b.id);
    queue.forEach((id) => { layer[id] = 0; });
    while (queue.length) {
      const id = queue.shift();
      (adj[id] || []).forEach((next) => {
        layer[next] = Math.max(layer[next] || 0, (layer[id] || 0) + 1);
        incoming[next] = incoming[next].filter((x) => x !== id);
        if (incoming[next].length === 0) queue.push(next);
      });
    }
    const rows = {};
    graph.blocks.forEach((b) => {
      const l = layer[b.id] || 0;
      const r = rows[l] || 0;
      if (!positions[b.id]) {
        positions[b.id] = { x: 60 + l * 260, y: 50 + r * 130 };
      }
      rows[l] = r + 1;
    });
  }

  function nodeSize() { return { w: 190, h: 70 }; }

  function render() {
    if (!graph.blocks.length) {
      setStatus("Empty graph. Add a block from the palette.", "");
    }
    while (world.firstChild) world.removeChild(world.firstChild);
    world.setAttribute("transform", `translate(${view.x},${view.y}) scale(${view.k})`);
    const defs = document.createElementNS(NS, "defs");
    const marker = document.createElementNS(NS, "marker");
    marker.setAttribute("id", "arrow");
    marker.setAttribute("viewBox", "0 0 10 10");
    marker.setAttribute("refX", "9");
    marker.setAttribute("refY", "5");
    marker.setAttribute("markerWidth", "6");
    marker.setAttribute("markerHeight", "6");
    marker.setAttribute("orient", "auto-start-reverse");
    const arrow = document.createElementNS(NS, "path");
    arrow.setAttribute("d", "M 0 0 L 10 5 L 0 10 z");
    arrow.setAttribute("fill", "var(--border-strong)");
    marker.appendChild(arrow);
    defs.appendChild(marker);
    world.appendChild(defs);

    graph.edges.forEach((e, i) => {
      const a = positions[e.from], b = positions[e.to];
      if (!a || !b) return;
      const s = nodeSize();
      const line = document.createElementNS(NS, "line");
      line.setAttribute("x1", a.x + s.w);
      line.setAttribute("y1", a.y + s.h / 2);
      line.setAttribute("x2", b.x);
      line.setAttribute("y2", b.y + s.h / 2);
      line.setAttribute("class", "canvas-edge" + (selected && selected.type === "edge" && selected.id === i ? " selected" : ""));
      line.setAttribute("marker-end", "url(#arrow)");
      line.dataset.edge = String(i);
      line.addEventListener("mousedown", (ev) => {
        ev.stopPropagation();
        selected = { type: "edge", id: i };
        render();
      });
      world.appendChild(line);
    });

    const kinds = byKind();
    graph.blocks.forEach((b) => {
      const p = positions[b.id] || { x: 60, y: 50 };
      const s = nodeSize();
      const g = document.createElementNS(NS, "g");
      g.setAttribute("class", "canvas-node" + (selected && selected.type === "node" && selected.id === b.id ? " selected" : "") + (kinds[b.kind] && kinds[b.kind].human_gate ? " human" : ""));
      g.setAttribute("transform", `translate(${p.x},${p.y})`);
      const rect = document.createElementNS(NS, "rect");
      rect.setAttribute("width", s.w);
      rect.setAttribute("height", s.h);
      rect.setAttribute("rx", "8");
      g.appendChild(rect);
      const title = document.createElementNS(NS, "text");
      title.setAttribute("x", "10");
      title.setAttribute("y", "22");
      title.textContent = truncate(b.title || (kinds[b.kind] && kinds[b.kind].title) || b.kind, 26);
      g.appendChild(title);
      const kind = document.createElementNS(NS, "text");
      kind.setAttribute("x", "10");
      kind.setAttribute("y", "40");
      kind.setAttribute("class", "kind");
      kind.textContent = b.kind;
      g.appendChild(kind);
      const spec = kinds[b.kind];
      if (spec) {
        const ports = document.createElementNS(NS, "text");
        ports.setAttribute("x", "10");
        ports.setAttribute("y", "58");
        ports.setAttribute("class", "ports");
        const ins = (spec.inputs || []).map((p) => p.name + ":" + p.type).join(" ");
        const outs = (spec.outputs || []).map((p) => p.name + ":" + p.type).join(" ");
        ports.textContent = truncate((ins ? "in " + ins + "  " : "") + (outs ? "out " + outs : ""), 34);
        g.appendChild(ports);
      }
      g.addEventListener("mousedown", (ev) => {
        if (!editable) return;
        ev.stopPropagation();
        startDrag(b.id, ev);
      });
      g.addEventListener("click", (ev) => {
        ev.stopPropagation();
        selectNode(b.id);
      });
      world.appendChild(g);
    });
    if (selected && selected.type === "node") selectNode(selected.id, true);
    else clearInspector();
  }

  function truncate(s, n) { return s.length > n ? s.slice(0, n - 1) + "…" : s; }

  function selectNode(id, silent) {
    const b = graph.blocks.find((x) => x.id === id);
    if (!b) return;
    selected = { type: "node", id };
    if (!silent) render();
    const spec = byKind()[b.kind];
    inspectorEmpty.hidden = true;
    inspectorBody.hidden = false;
    inspTitle.value = b.title || (spec && spec.title) || "";
    inspInstructions.value = (b.config && b.config.instructions) || "";
    inspQueries.value = ((b.config && b.config.queries) || []).join("\n");
    inspAcceptance.value = ((b.config && b.config.acceptance) || []).join("\n");
    inspMeta.textContent = "";
    if (spec) {
      const line = document.createElement("div");
      line.textContent = `${spec.kind} · ${spec.capability}${spec.human_gate ? " · human gate" : ""}`;
      inspMeta.appendChild(line);
      const tools = document.createElement("div");
      tools.textContent = "tools: " + ((spec.default_tools || []).join(", ") || "none");
      inspMeta.appendChild(tools);
    }
    connectBox.hidden = true;
    connectTarget.textContent = "";
    graph.blocks.forEach((other) => {
      if (other.id === id) return;
      const opt = document.createElement("option");
      opt.value = other.id;
      opt.textContent = other.title || other.kind;
      connectTarget.appendChild(opt);
    });
    setStatus("", "");
  }

  function clearInspector() {
    inspectorEmpty.hidden = false;
    inspectorBody.hidden = true;
    selected = null;
  }

  let drag = null;
  function startDrag(id, ev) {
    if (!editable) return;
    ev.preventDefault();
    const p = positions[id] || { x: 0, y: 0 };
    drag = { id, sx: ev.clientX, sy: ev.clientY, ox: p.x, oy: p.y, moved: false };
  }
  window.addEventListener("mousemove", (ev) => {
    if (!drag) return;
    const dx = (ev.clientX - drag.sx) / view.k;
    const dy = (ev.clientY - drag.sy) / view.k;
    if (Math.abs(dx) + Math.abs(dy) > 1) drag.moved = true;
    positions[drag.id] = { x: drag.ox + dx, y: drag.oy + dy };
    render();
  });
  window.addEventListener("mouseup", () => {
    if (drag && drag.moved) pushHistory();
    drag = null;
  });

  function applyInspector() {
    if (!selected || selected.type !== "node") return;
    const b = graph.blocks.find((x) => x.id === selected.id);
    if (!b) return;
    b.title = inspTitle.value.trim();
    b.config = b.config || {};
    if (inspInstructions.value.trim()) b.config.instructions = inspInstructions.value.trim();
    else delete b.config.instructions;
    const queries = inspQueries.value.split("\n").map((s) => s.trim()).filter(Boolean);
    if (queries.length) b.config.queries = queries;
    else delete b.config.queries;
    const acc = inspAcceptance.value.split("\n").map((s) => s.trim()).filter(Boolean);
    if (acc.length) b.config.acceptance = acc;
    else delete b.config.acceptance;
    pushHistory();
    render();
    setStatus("Block updated.", "ok");
  }

  function compatibleEdge(from, to) {
    const kinds = byKind();
    const a = kinds[from.kind], b = kinds[to.kind];
    if (!a || !b) return null;
    for (const out of a.outputs || []) {
      for (const inp of b.inputs || []) {
        if (out.type === inp.type) return { from_port: out.name, to_port: inp.name };
      }
    }
    return null;
  }

  function addEdge(fromID, toID) {
    if (fromID === toID) { setStatus("A block cannot connect to itself.", "bad"); return; }
    if (graph.edges.some((e) => e.from === fromID && e.to === toID)) {
      setStatus("That edge already exists.", "bad"); return;
    }
    const from = graph.blocks.find((b) => b.id === fromID);
    const to = graph.blocks.find((b) => b.id === toID);
    if (!from || !to) return;
    const ports = compatibleEdge(from, to);
    if (!ports) { setStatus(`No compatible ports: ${from.kind} → ${to.kind}.`, "bad"); return; }
    graph.edges.push({ from: fromID, to: toID, ...ports });
    pushHistory();
    render();
    setStatus("Edge added.", "ok");
  }

  function addBlock(kind) {
    if (!editable) return;
    const spec = byKind()[kind];
    if (!spec) return;
    let n = 1;
    const base = String(kind).replace(/_/g, "-");
    let id = base;
    while (graph.blocks.some((b) => b.id === id)) id = base + "-" + (++n);
    graph.blocks.push({ id, kind, title: spec.title, config: {} });
    positions[id] = { x: 80 + (Object.keys(positions).length % 3) * 250, y: 60 + Math.floor(Object.keys(positions).length / 3) * 120 };
    selected = { type: "node", id };
    pushHistory();
    render();
    selectNode(id);
  }

  function deleteSelected() {
    if (!editable || !selected) return;
    if (selected.type === "edge") {
      graph.edges.splice(selected.id, 1);
    } else {
      const id = selected.id;
      graph.blocks = graph.blocks.filter((b) => b.id !== id);
      graph.edges = graph.edges.filter((e) => e.from !== id && e.to !== id);
      delete positions[id];
    }
    selected = null;
    pushHistory();
    render();
    setStatus("Deleted.", "ok");
  }

  async function save() {
    if (!editable) return;
    setStatus("Saving…", "");
    try {
      const res = await fetch(`/ui/cycles/${cycleID}/graph`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ blocks: graph.blocks, edges: graph.edges }),
      });
      const data = await res.json();
      if (!res.ok || !data.ok) throw new Error(data.error || res.statusText);
      setStatus("Saved.", "ok");
    } catch (err) {
      setStatus("Save failed: " + err.message, "bad");
    }
  }

  async function start() {
    if (!editable) return;
    if (!confirm("Start this cycle? The graph will be compiled into tasks and the scheduler will run it.")) return;
    setStatus("Starting…", "");
    try {
      const res = await fetch(`/ui/cycles/${cycleID}/start`, { method: "POST" });
      const data = await res.json();
      if (!res.ok || !data.ok) throw new Error(data.error || res.statusText);
      editable = false;
      setStatus(`Started · ${data.jobs ? data.jobs.length : 0} job(s).`, "ok");
      setTimeout(() => location.reload(), 900);
    } catch (err) {
      setStatus("Start failed: " + err.message, "bad");
    }
  }

  // Pan and zoom.
  svg.addEventListener("mousedown", (ev) => {
    if (ev.target.closest(".canvas-node")) return;
    svg.classList.add("panning");
    const start = { x: ev.clientX, y: ev.clientY, vx: view.x, vy: view.y };
    const move = (m) => {
      view.x = start.vx + (m.clientX - start.x);
      view.y = start.vy + (m.clientY - start.y);
      world.setAttribute("transform", `translate(${view.x},${view.y}) scale(${view.k})`);
    };
    const up = () => {
      svg.classList.remove("panning");
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  });
  svg.addEventListener("wheel", (ev) => {
    ev.preventDefault();
    const rect = svg.getBoundingClientRect();
    const mx = ev.clientX - rect.left, my = ev.clientY - rect.top;
    const wx = (mx - view.x) / view.k, wy = (my - view.y) / view.k;
    const k = Math.min(2.2, Math.max(0.35, view.k * (ev.deltaY > 0 ? 0.9 : 1.1)));
    view = { k, x: mx - wx * k, y: my - wy * k };
    world.setAttribute("transform", `translate(${view.x},${view.y}) scale(${view.k})`);
  }, { passive: false });

  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") { selected = null; render(); }
    if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === "z") {
      ev.preventDefault();
      const raw = history.pop();
      if (raw) restore(raw);
    }
    if (ev.key === "Delete" || ev.key === "Backspace") {
      if (document.activeElement && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName)) return;
      deleteSelected();
    }
  });

  palette.addEventListener("click", (ev) => {
    const btn = ev.target.closest("[data-kind]");
    if (btn) addBlock(btn.dataset.kind);
  });
  document.getElementById("insp-apply").addEventListener("click", applyInspector);
  document.getElementById("insp-delete").addEventListener("click", deleteSelected);
  document.getElementById("insp-connect").addEventListener("click", () => {
    if (!selected || selected.type !== "node") return;
    connectBox.hidden = !connectBox.hidden;
  });
  document.getElementById("insp-connect-do").addEventListener("click", () => {
    if (!selected || selected.type !== "node") return;
    if (!connectTarget.value) return;
    addEdge(selected.id, connectTarget.value);
    connectBox.hidden = true;
  });
  document.getElementById("canvas-save").addEventListener("click", save);
  document.getElementById("canvas-start").addEventListener("click", start);
  document.getElementById("canvas-undo").addEventListener("click", () => {
    const raw = history.pop();
    if (raw) restore(raw);
  });
  document.getElementById("canvas-cycle-select").addEventListener("change", (ev) => {
    location.href = `/ui/projects/${projectID}/canvas?cycle=${encodeURIComponent(ev.target.value)}`;
  });

  fetch(`/ui/cycles/${cycleID}/graph.json`, { headers: { Accept: "application/json" } })
    .then((r) => { if (!r.ok) throw new Error(r.statusText); return r.json(); })
    .then((data) => {
      graph = data.graph || { blocks: [], edges: [] };
      registry = data.registry || [];
      editable = !!data.editable;
      layout();
      render();
      setStatus(editable ? "Draft" : "Read-only", "");
    })
    .catch((err) => setStatus("Load failed: " + err.message, "bad"));
})();
