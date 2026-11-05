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

  /* ---------- auto refresh ---------- */
  $$("[data-auto-refresh]").forEach((el) => {
    const ms = Number(el.getAttribute("data-auto-refresh")) || 5000;
    setTimeout(() => location.reload(), ms);
  });

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

  /* ---------- boot ---------- */
  function boot() {
    markNav();
    wireDashboard();
    wireWatch();
    wirePalette();
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
