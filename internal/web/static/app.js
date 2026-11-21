// SPDX-License-Identifier: GPL-3.0-or-later
// Small helpers for the RepoShelf UI: command palette (⌘K, T-2.7), live
// updates via server-sent events, copy buttons, theme toggle. No framework.
(function () {
  "use strict";
  var $ = function (s, r) { return (r || document).querySelector(s); };

  // Live updates: re-dispatch server events as DOM events for htmx triggers.
  if (window.EventSource) {
    try {
      var es = new EventSource("/events");
      ["repo", "job", "reindex", "cleanup", "collection", "build"].forEach(function (k) {
        es.addEventListener(k, function () { document.body.dispatchEvent(new CustomEvent("sse-" + k)); });
      });
      // Notifications (V-2): a short notice while the page is open.
      es.addEventListener("notify", function (e) {
        var text = "";
        try { text = JSON.parse(e.data).text || ""; } catch (err) { return; }
        if (!text) return;
        var t = document.createElement("div");
        t.className = "toast";
        t.setAttribute("role", "status");
        t.textContent = text;
        t.addEventListener("click", function () { location.href = "/notifications"; });
        document.body.appendChild(t);
        setTimeout(function () { t.classList.add("toast-hide"); }, 6000);
        setTimeout(function () { t.remove(); }, 6600);
      });
    } catch (e) {}
  }

  document.addEventListener("click", function (e) {
    var t = e.target.closest("[data-copy]");
    if (t) {
      var input = $(t.getAttribute("data-copy"));
      if (input) {
        input.select();
        var text = input.value;
        (navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject()).catch(function () { document.execCommand("copy"); });
        t.textContent = "Kopiert ✓";
        setTimeout(function () { t.textContent = "Kopieren"; }, 1600);
      }
    }
    var tag = e.target.closest("[data-add-tag]");
    if (tag) {
      var f = $("#tags");
      var v = tag.getAttribute("data-add-tag");
      if (f && f.value.split(",").map(function (s) { return s.trim(); }).indexOf(v) < 0) {
        f.value = f.value.trim() ? f.value.trim().replace(/,$/, "") + ", " + v : v;
      }
    }
    if (e.target.closest("[data-theme-toggle]")) {
      var cur = document.documentElement.getAttribute("data-theme") === "light" ? "dark" : "light";
      document.documentElement.setAttribute("data-theme", cur);
      try { localStorage.setItem("rs-theme", cur); } catch (x) {}
    }
    if (e.target.closest("[data-nav-toggle]")) {
      var sb = $("#sidebar");
      sb.classList.toggle("open");
      e.target.setAttribute("aria-expanded", sb.classList.contains("open"));
    }
    if (e.target.closest("[data-back]")) { e.preventDefault(); history.back(); }
  });

  // Loading bar while the next page is requested (T-9.9).
  function navigating() { document.documentElement.classList.add("navigating"); }
  document.addEventListener("click", function (e) {
    var a = e.target.closest("a[href]");
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey) return;
    var href = a.getAttribute("href");
    if (!href || href.charAt(0) === "#" || a.target === "_blank" || a.hasAttribute("download") || a.hasAttribute("hx-get")) return;
    if (a.origin && a.origin !== location.origin) return;
    if (/\/(asset|action|raw|artifact)\b/.test(a.pathname)) return; // downloads keep the page
    navigating();
  });
  document.addEventListener("submit", function (e) {
    if (!e.defaultPrevented && !e.target.hasAttribute("hx-get") && !e.target.hasAttribute("hx-post")) navigating();
  });
  window.addEventListener("pageshow", function () { document.documentElement.classList.remove("navigating"); });

  document.addEventListener("change", function (e) {
    if (e.target.matches("[data-autosubmit]")) e.target.form.submit();
  });

  // Command palette.
  var pal = $("#palette"), input = $("#palette-input"), list = $("#palette-results");
  var commands = [
    { Title: "Repo hinzufügen", Sub: "Link einfügen und sichern", URL: "/add" },
    { Title: "Bibliothek", Sub: "Alle Repos", URL: "/" },
    { Title: "Schlagwörter", Sub: "Hierarchie und Umbenennen", URL: "/tags" },
    { Title: "Beziehungen", Sub: "Graph", URL: "/graph" },
    { Title: "Code-Suche", Sub: "git grep über alle Repos", URL: "/code" },
    { Title: "Upstream weg", Sub: "Repos, deren Original verschwunden ist", URL: "/?status=gone" },
    { Title: "Lösch-Prüfung", Sub: "Was würde die Bereinigung entfernen?", URL: "/cleanup" },
    { Title: "Papierkorb", Sub: "", URL: "/trash" },
    { Title: "Einstellungen", Sub: "", URL: "/settings" },
    { Title: "KI-Zugriff & Tokens", Sub: "MCP, Git-Zugang", URL: "/access" },
    { Title: "Verlauf", Sub: "Wer hat was geändert", URL: "/activity" }
  ];
  var sel = 0, items = [], timer;
  function draw(hits) {
    items = hits; sel = 0; list.innerHTML = "";
    hits.forEach(function (h, i) {
      var li = document.createElement("li");
      li.setAttribute("role", "option");
      li.setAttribute("aria-selected", i === 0);
      var a = document.createElement("a");
      a.href = h.URL;
      var b = document.createElement("span"); b.textContent = h.Title;
      var s = document.createElement("small"); s.textContent = h.Sub || "";
      a.appendChild(b); a.appendChild(s); li.appendChild(a); list.appendChild(li);
    });
  }
  function mark() {
    Array.prototype.forEach.call(list.children, function (li, i) { li.setAttribute("aria-selected", i === sel); });
    if (list.children[sel]) list.children[sel].scrollIntoView({ block: "nearest" });
  }
  function open() { pal.hidden = false; input.value = ""; draw(commands); input.focus(); }
  function close() { pal.hidden = true; }
  if (pal) {
    document.addEventListener("keydown", function (e) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); pal.hidden ? open() : close(); return; }
      if (pal.hidden) return;
      if (e.key === "Escape") close();
      else if (e.key === "ArrowDown") { sel = Math.min(sel + 1, items.length - 1); mark(); e.preventDefault(); }
      else if (e.key === "ArrowUp") { sel = Math.max(sel - 1, 0); mark(); e.preventDefault(); }
      else if (e.key === "Enter" && items[sel]) { location.href = items[sel].URL; }
    });
    pal.addEventListener("click", function (e) { if (e.target === pal) close(); });
    input.addEventListener("input", function () {
      clearTimeout(timer);
      var q = input.value.trim();
      var local = commands.filter(function (c) { return c.Title.toLowerCase().indexOf(q.toLowerCase()) >= 0; });
      if (!q) { draw(commands); return; }
      timer = setTimeout(function () {
        fetch("/search?q=" + encodeURIComponent(q)).then(function (r) { return r.json(); })
          .then(function (hits) { draw((hits || []).concat(local)); }).catch(function () { draw(local); });
      }, 150);
    });
    var kbd = document.querySelector(".search kbd");
    if (kbd) { kbd.style.cursor = "pointer"; kbd.addEventListener("click", open); }
  }
})();
