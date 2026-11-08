// SPDX-License-Identifier: GPL-3.0-or-later
// Relation graph (T-9.4): a small force layout in SVG, no library.
(function () {
  "use strict";
  var svg = document.getElementById("graph");
  if (!svg) return;
  var NS = "http://www.w3.org/2000/svg";
  var colors = { own: "var(--accent)", mirror: "var(--kind-mirror)", fork: "var(--kind-fork)" };
  fetch("/graph.json").then(function (r) { return r.json(); }).then(function (g) {
    if (!g.nodes || !g.nodes.length) { document.getElementById("graph-empty").hidden = false; svg.style.display = "none"; return; }
    var W = svg.clientWidth || 900, H = 640;
    var byId = {};
    g.nodes.forEach(function (n, i) {
      var a = 2 * Math.PI * i / g.nodes.length;
      n.x = W / 2 + Math.cos(a) * W / 4; n.y = H / 2 + Math.sin(a) * H / 4; n.vx = 0; n.vy = 0;
      byId[n.ID] = n;
    });
    var edges = (g.edges || []).filter(function (e) { return byId[e.From] && byId[e.To]; });
    var lines = edges.map(function (e) {
      var l = document.createElementNS(NS, "line");
      if (e.State === "suggested") l.setAttribute("class", "suggested");
      var t = document.createElementNS(NS, "title"); t.textContent = e.Type; l.appendChild(t);
      svg.appendChild(l); return l;
    });
    var groups = g.nodes.map(function (n) {
      var gr = document.createElementNS(NS, "g");
      var c = document.createElementNS(NS, "circle");
      c.setAttribute("r", 9); c.setAttribute("fill", colors[n.Kind] || "var(--text-muted)");
      var tx = document.createElementNS(NS, "text"); tx.textContent = n.Label; tx.setAttribute("x", 13); tx.setAttribute("y", 4);
      gr.appendChild(c); gr.appendChild(tx); svg.appendChild(gr);
      c.addEventListener("click", function () { if (!n.dragged) location.href = "/repo/" + n.ID; });
      c.addEventListener("pointerdown", function (ev) {
        n.fixed = true; n.dragged = false; c.setPointerCapture(ev.pointerId);
        c.onpointermove = function (m) { var r = svg.getBoundingClientRect(); n.x = m.clientX - r.left; n.y = m.clientY - r.top; n.dragged = true; kick(); };
        c.onpointerup = function () { c.onpointermove = null; n.fixed = false; };
      });
      return gr;
    });
    var steps = 0;
    function tick() {
      g.nodes.forEach(function (a) {
        g.nodes.forEach(function (b) {
          if (a === b) return;
          var dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy + 0.01;
          var f = 1800 / d2; a.vx += dx * f / Math.sqrt(d2); a.vy += dy * f / Math.sqrt(d2);
        });
        a.vx += (W / 2 - a.x) * 0.002; a.vy += (H / 2 - a.y) * 0.002;
      });
      edges.forEach(function (e) {
        var a = byId[e.From], b = byId[e.To], dx = b.x - a.x, dy = b.y - a.y, d = Math.sqrt(dx * dx + dy * dy) || 1;
        var f = (d - 120) * 0.01; a.vx += dx / d * f; a.vy += dy / d * f; b.vx -= dx / d * f; b.vy -= dy / d * f;
      });
      g.nodes.forEach(function (n) {
        if (!n.fixed) { n.x += n.vx; n.y += n.vy; }
        n.vx *= 0.6; n.vy *= 0.6;
        n.x = Math.max(12, Math.min(W - 120, n.x)); n.y = Math.max(12, Math.min(H - 12, n.y));
      });
      edges.forEach(function (e, i) {
        lines[i].setAttribute("x1", byId[e.From].x); lines[i].setAttribute("y1", byId[e.From].y);
        lines[i].setAttribute("x2", byId[e.To].x); lines[i].setAttribute("y2", byId[e.To].y);
      });
      g.nodes.forEach(function (n, i) { groups[i].setAttribute("transform", "translate(" + n.x + "," + n.y + ")"); });
      if (++steps < 300) requestAnimationFrame(tick);
    }
    function kick() { if (steps >= 300) { steps = 250; requestAnimationFrame(tick); } }
    tick();
  });
})();
