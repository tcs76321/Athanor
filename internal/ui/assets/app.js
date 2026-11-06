/* Athanor UI — small progressive-enhancement layer.
 *
 * Server-rendered HTML is the source of truth. This file only adds live
 * updates, keyboard shortcuts, confirmations, and the command palette. The
 * page works with JavaScript disabled.
 */
(function () {
  "use strict";

  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));

  /* ---------- navigation state ---------- */
  function markNav() {
    const path = location.pathname;
    $$("header nav a, .rail nav a").forEach((a) => {
      const href = a.getAttribute("href");
      if (!href || href === "#") return;
      const active =
        href === "/ui"
          ? path === "/ui"
          : path === href || path.startsWith(href + "/");
      if (active) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
  }

  /* ---------- toasts ---------- */
  function toast(message, kind) {
    let host = $(".toasts");
    if (!host) {
      host = document.createElement("div");
      host.className = "toasts";
      host.setAttribute("role", "status");
      host.setAttribute("aria-live", "polite");
      host.setAttribute("aria-atomic", "false");
      document.body.appendChild(host);
    }
    const el = document.createElement("div");
    el.className = "toast" + (kind ? " " + kind : "");
    el.textContent = String(message || "");
    host.appendChild(el);
    setTimeout(() => el.remove(), 6000);
  }
  window.athanorToast = toast;

  /* ---------- confirmations ---------- */
  document.addEventListener("click", (ev) => {
    const el = ev.target.closest("[data-confirm]");
    if (!el) return;
    if (!window.confirm(el.getAttribute("data-confirm"))) {
      ev.preventDefault();
      ev.stopPropagation();
    }
  });

  /* ---------- history back button in the error shell ---------- */
  document.addEventListener("click", (ev) => {
    const el = ev.target.closest("[data-back]");
    if (!el) return;
    ev.preventDefault();
    history.back();
  });

  /* ---------- copy buttons ---------- */
  document.addEventListener("click", (ev) => {
    const el = ev.target.closest("[data-copy]");
    if (!el) return;
    ev.preventDefault();
    const text = el.getAttribute("data-copy");
    if (!text) return;
    navigator.clipboard.writeText(text).then(
      () => toast("Copied", "ok"),
      () => toast("Copy failed", "bad")
    );
  });

  /* ---------- theme picker ---------- */
  $$("[data-theme-form] select").forEach((sel) => {
    sel.addEventListener("change", () => {
      const form = sel.closest("form");
      if (form) form.submit();
    });
  });
  $$("[data-theme-preview]").forEach((el) => {
    const theme = el.getAttribute("data-theme-preview");
    el.addEventListener("mouseenter", () => document.documentElement.setAttribute("data-theme", theme));
    el.addEventListener("focus", () => document.documentElement.setAttribute("data-theme", theme));
    el.addEventListener("mouseleave", () => document.documentElement.setAttribute("data-theme", document.documentElement.dataset.baseTheme || "monokai"));
    el.addEventListener("blur", () => document.documentElement.setAttribute("data-theme", document.documentElement.dataset.baseTheme || "monokai"));
  });

  /* ---------- in-place live cards ---------- */
  // Status panels used to call location.reload() on a timer, which discarded
  // open <details> panels, scroll position, and anything the operator was
  // reading. Poll the same page, swap only the named card's content, and
  // restore any open details by their summary text.
  function wireLiveCards() {
    $$("[data-live-card]").forEach((el) => {
      const key = el.getAttribute("data-live-card");
      const ms = Number(el.getAttribute("data-live-interval")) || 3000;
      let stopped = false;

      const schedule = () => {
        if (!stopped) setTimeout(tick, ms);
      };

      async function tick() {
        if (document.hidden) {
          schedule();
          return;
        }
        try {
          const r = await fetch(window.location.href, {
            cache: "no-store",
            headers: { "HX-Request": "1" },
          });
          if (!r.ok) {
            schedule();
            return;
          }
          const doc = new DOMParser().parseFromString(await r.text(), "text/html");
          const next = Array.from(doc.querySelectorAll("[data-live-card]"))
            .find((n) => n.getAttribute("data-live-card") === key);
          if (!next) {
            // The stage finished and the page moved on. Reload once so the
            // next stage (choose/plan/complete) can render.
            stopped = true;
            window.location.reload();
            return;
          }
          const open = new Set(
            Array.from(el.querySelectorAll("details[open]"))
              .map((d) => (d.querySelector("summary") || {}).textContent || "")
          );
          el.className = next.className;
          el.innerHTML = next.innerHTML;
          el.querySelectorAll("details").forEach((d) => {
            const s = (d.querySelector("summary") || {}).textContent || "";
            if (open.has(s)) d.open = true;
          });
        } catch (_) {}
        schedule();
      }

      schedule();
    });
  }

  /* ---------- desktop notifications ---------- */
  const notifyBtn = $("#enable-notifications");
  if (notifyBtn) {
    const status = $("#notify-status");
    const render = () => {
      if (!("Notification" in window)) {
        notifyBtn.disabled = true;
        notifyBtn.textContent = "Desktop notifications unavailable";
        return;
      }
      if (Notification.permission === "granted") {
        notifyBtn.textContent = "Desktop notifications enabled";
        notifyBtn.disabled = true;
      } else if (Notification.permission === "denied") {
        notifyBtn.textContent = "Desktop notifications blocked in the browser";
        notifyBtn.disabled = true;
      }
    };
    notifyBtn.addEventListener("click", () => {
      Notification.requestPermission().then(render);
      if (status) status.textContent = "The browser will ask for permission.";
    });
    render();
  }

  /* ---------- dashboard live updates ---------- */
  function wireDashboard() {
    const alertEl = $("#alert-count");
    const reviewEl = $("#review-count");
    if (!alertEl && !reviewEl) return;

    async function refresh() {
      try {
        const r = await fetch("/ui/notifications/count", { cache: "no-store" });
        const c = await r.json();
        if (alertEl) {
          alertEl.textContent = String(c.total || 0);
          alertEl.hidden = !c.total;
        }
        if (reviewEl) {
          reviewEl.textContent = String(c.reviews || 0);
          reviewEl.hidden = !c.reviews;
        }
      } catch (_) {}
    }
    refresh();
    setInterval(refresh, 30000);

    try {
      const es = new EventSource("/ui/events/stream");
      es.addEventListener("event", (e) => {
        let p;
        try {
          p = JSON.parse(e.data);
        } catch (_) {
          return;
        }
        let d;
        try {
          d = JSON.parse(p.data);
        } catch (_) {
          return;
        }
        if (d.event === "transition" && d.to) {
          const el = document.getElementById("phase-" + p.job_id);
          if (el) el.textContent = d.to;
        }
        let title = "";
        let body = "";
        if (d.event === "hitl_request") {
          title = "Approval needed";
          body = (d.type || "") + " (" + (d.severity || "") + ")";
        } else if (d.event === "job_failed") {
          title = "Job failed";
          body = d.error || "a job failed";
        } else if (d.event === "alarm_raised") {
          title = "Athanor alarm";
          body = d.message || "";
        }
        if (title && "Notification" in window && Notification.permission === "granted") {
          try {
            new Notification("Athanor · " + title, { body: String(body).slice(0, 180) });
          } catch (_) {}
        }
        refresh();
      });
      es.onerror = () => {
        es.close();
        setTimeout(wireDashboard, 5000);
      };
    } catch (_) {}
  }

  /* ---------- watch stream ---------- */
  function wireWatch() {
    const root = document.getElementById("watch");
    if (!root) return;
    const jobID = root.getAttribute("data-job");
    if (!jobID) return;
    const pre = document.getElementById("tokens");
    let stick = true;
    if (pre) {
      pre.addEventListener("scroll", () => {
        stick = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 24;
      });
    }
    function eventLine(p) {
      let d = {};
      try {
        d = JSON.parse(p.data);
      } catch (_) {}
      const label = d.event || p.category || "event";
      const keys = ["phase", "state", "to", "winner", "passed", "score", "reason", "decision", "unit", "iter"];
      const parts = [];
      for (const k of keys) {
        if (d[k] !== undefined && d[k] !== "" && parts.length < 4) parts.push(k + "=" + d[k]);
      }
      const ts = p.ts ? String(p.ts).slice(11, 19) + " " : "";
      return ts + label + (parts.length ? " · " + parts.join(" · ") : "");
    }
    let es;
    function connect() {
      es = new EventSource("/ui/jobs/" + encodeURIComponent(jobID) + "/stream");
      es.addEventListener("token", (e) => {
        if (!pre) return;
        let t;
        try {
          t = JSON.parse(e.data);
        } catch (_) {
          return;
        }
        pre.textContent += t;
        if (stick) pre.scrollTop = pre.scrollHeight;
      });
      es.addEventListener("event", (e) => {
        let p;
        try {
          p = JSON.parse(e.data);
        } catch (_) {
          return;
        }
        const host = document.getElementById("events");
        if (!host) return;
        const div = document.createElement("div");
        div.className = "ev";
        div.textContent = eventLine(p);
        host.prepend(div);
      });
      es.onerror = () => {
        es.close();
        const s = document.getElementById("stream-state");
        if (s) s.textContent = "reconnecting…";
        setTimeout(connect, 3000);
      };
      es.onopen = () => {
        const s = document.getElementById("stream-state");
        if (s) s.textContent = "live";
      };
    }
    connect();
  }

  /* ---------- command palette ---------- */
  function wirePalette() {
    const box = document.getElementById("palette");
    if (!box) return;
    const input = $("#palette-input", box);
    const results = $("#palette-results", box);
    let items = [];
    let active = 0;

    function close() {
      box.classList.remove("open");
      box.setAttribute("aria-hidden", "true");
    }
    function open() {
      box.classList.add("open");
      box.setAttribute("aria-hidden", "false");
      if (input) {
        input.value = "";
        input.focus();
      }
      search("");
    }
    function render() {
      if (!results) return;
      results.innerHTML = "";
      items.forEach((it, i) => {
        const a = document.createElement("a");
        a.href = it.url;
        a.className = i === active ? "active" : "";
        const label = document.createElement("span");
        label.textContent = it.label;
        const kind = document.createElement("span");
        kind.className = "kind";
        kind.textContent = it.kind || "";
        a.appendChild(label);
        a.appendChild(kind);
        results.appendChild(a);
      });
    }
    async function search(q) {
      try {
        const r = await fetch("/ui/search?format=json&q=" + encodeURIComponent(q || ""), {
          headers: { Accept: "application/json" },
        });
        const body = await r.json();
        items = body.results || [];
        active = 0;
        render();
      } catch (_) {
        items = [];
        render();
      }
    }
    document.addEventListener("keydown", (e) => {
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "k") {
        e.preventDefault();
        open();
      } else if (e.key === "Escape" && box.classList.contains("open")) {
        close();
      }
    });
    box.addEventListener("click", (e) => {
      if (e.target === box) close();
    });
    if (input) {
      input.addEventListener("input", () => search(input.value));
      input.addEventListener("keydown", (e) => {
        if (e.key === "ArrowDown") {
          e.preventDefault();
          active = Math.min(items.length - 1, active + 1);
          render();
        } else if (e.key === "ArrowUp") {
          e.preventDefault();
          active = Math.max(0, active - 1);
          render();
        } else if (e.key === "Enter" && items[active]) {
          location.href = items[active].url;
        }
      });
    }
    $$("[data-palette-open]").forEach((b) => b.addEventListener("click", open));
  }

  /* ---------- HTMX errors ---------- */
  document.addEventListener("htmx:responseError", (e) => {
    const xhr = e.detail && e.detail.xhr;
    toast("Request failed" + (xhr ? " (" + xhr.status + ")" : ""), "bad");
  });
  document.addEventListener("htmx:sendError", () => toast("Network error", "bad"));

  /* ---------- field refinement ---------- */
  function wireRefiners() {
    $$("[data-refine-field]").forEach((field) => {
      if (field.dataset.refineReady) return;
      field.dataset.refineReady = "1";
      const wrap = document.createElement("div");
      wrap.className = "refine-wrap";
      field.parentNode.insertBefore(wrap, field);
      wrap.appendChild(field);

      const bar = document.createElement("div");
      bar.className = "refine-bar";
      const wand = document.createElement("button");
      wand.type = "button";
      wand.className = "refine-btn";
      wand.title = "Refine this field for Athanor";
      wand.setAttribute("aria-label", "Refine this field");
      wand.textContent = "✨";
      const revert = document.createElement("button");
      revert.type = "button";
      revert.className = "refine-btn secondary";
      revert.textContent = "Revert";
      revert.disabled = true;
      bar.append(wand, revert);

      const status = document.createElement("div");
      status.className = "refine-status";
      status.setAttribute("aria-live", "polite");
      const questions = document.createElement("div");
      questions.className = "refine-questions";
      questions.hidden = true;
      wrap.append(bar, status, questions);

      let original = null;
      wand.addEventListener("click", async () => {
        const text = String(field.value || "").trim();
        questions.hidden = true;
        questions.replaceChildren();
        if (original === null) original = field.value;
        wand.disabled = true;
        status.className = "refine-status";
        status.textContent = "Refining…";
        try {
          const resp = await fetch("/ui/assist", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              field: field.dataset.refineField,
              text,
              mode: field.dataset.refineMode || "improve",
            }),
          });
          const data = await resp.json().catch(() => ({}));
          if (!resp.ok) throw new Error(data.error || "Request failed (" + resp.status + ")");
          if (data.needs_clarification) {
            const title = document.createElement("b");
            title.textContent = "Athanor needs a bit more:";
            const list = document.createElement("ul");
            (data.questions || []).forEach((q) => {
              const li = document.createElement("li");
              li.textContent = q;
              list.appendChild(li);
            });
            const hint = document.createElement("p");
            hint.className = "muted small";
            hint.textContent = "Answer in the field, then press ✨ again.";
            questions.replaceChildren(title, list, hint);
            questions.hidden = false;
            status.textContent = "Needs clarification — answer the questions, then refine again.";
            status.classList.add("warn");
            field.focus();
            toast("Athanor needs a bit more — see the questions.");
            return;
          }
          if (!data.text) throw new Error("The model returned an empty suggestion");
          if (data.unchanged || String(field.value) === String(data.text)) {
            const why = (data.notes || []).join(" · ");
            status.textContent = "✓ Idealized — this field is complete and ideal." + (why ? " " + why : "");
            status.classList.add("ok");
            toast("Idealized ✓ — this field is already complete.");
            return;
          }
          field.value = data.text;
          field.dispatchEvent(new Event("input", { bubbles: true }));
          field.dispatchEvent(new Event("change", { bubbles: true }));
          revert.disabled = false;
          const notes = []
            .concat(data.notes || [])
            .concat((data.added_assumptions || []).map((x) => "assumption: " + x));
          status.textContent = notes.length ? notes.join(" · ") : "Refined.";
          status.classList.add("ok");
          toast("Field refined — review it, or revert.");
        } catch (err) {
          status.textContent = err.message || "Refinement failed";
          status.classList.add("bad");
        } finally {
          wand.disabled = false;
        }
      });

      revert.addEventListener("click", () => {
        if (original === null) return;
        field.value = original;
        field.dispatchEvent(new Event("input", { bubbles: true }));
        field.dispatchEvent(new Event("change", { bubbles: true }));
        original = null;
        revert.disabled = true;
        questions.hidden = true;
        questions.replaceChildren();
        status.className = "refine-status";
        status.textContent = "Reverted to your original text.";
      });
    });
  }

  /* ---------- boot ---------- */
  function boot() {
    markNav();
    wireDashboard();
    wireWatch();
    wirePalette();
    wireRefiners();
    wireLiveCards();
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
