// Prototype of the module graph. Same input as the live page (View.Nodes, View.Links{From,To,Count,Status}),
// so this drawing can replace internal/control/ui/static/graph.js without new data.
// Layout is deterministic: rank by dependency depth (cycles broken first), order rows by the barycenter of
// neighbours. No force simulation, so the same report always draws the same picture.
(function () {
  "use strict";
  var NS = "http://www.w3.org/2000/svg";
  var NODE_W = 132, NODE_H = 34, GAP_X = 28, ROW_H = 96, PAD = 24;
  var RANK = { violation: 0, advisory: 1, accepted: 2, allowed: 3, observed: 4 };
  var PROBLEM = { violation: true, advisory: true, accepted: true };
  var LABEL = { violation: "violation", advisory: "advisory", accepted: "accepted debt", allowed: "allowed", observed: "fine" };
  var MAX_ROW = 6; // modules per drawn line
  var FOCUS_LIMIT = 15; // above this many modules the graph starts on problems and their neighbours

  // ---------- data: real pilot reports, plus one synthetic 40-module repository ----------
  var paths = { cmd: "cmd/**", pkg_chaos: "pkg/chaos/**", pkg_container: "pkg/container/**", pkg_runtime: "pkg/runtime/**", pkg_tc: "pkg/tc/**", pkg_util: "pkg/util/**" };
  function pilot(pr) {
    var links = [
      ["cmd", "pkg_chaos", 8, "advisory", "6 of 8 imports critical: functional coupling to a module of undeclared volatility."],
      ["cmd", "pkg_runtime", 3, "advisory", "3 of 3 imports critical: functional coupling to a module of undeclared volatility."],
      ["cmd", "pkg_container", 2, "observed", "Contract coupling. Balanced."],
      ["pkg_chaos", "pkg_container", 40, pr ? "violation" : "advisory", pr ? "Part of a module cycle with pkg_container → pkg_chaos. Rule no-module-cycles." : "5 of 40 imports critical: functional coupling to a module of undeclared volatility."],
      ["pkg_chaos", "pkg_util", 2, "advisory", "2 of 2 imports critical."],
      ["pkg_runtime", "pkg_container", 20, "advisory", "15 of 20 imports critical."],
      ["pkg_runtime", "pkg_tc", 2, "advisory", "2 of 2 imports critical."]
    ];
    if (pr) links.push(["pkg_container", "pkg_chaos", 1, "violation", "Closes the cycle. pkg/container/flags.go:3. Only with PR #4."]);
    return {
      nodes: Object.keys(paths).map(function (n) { return { Name: n, Path: paths[n] }; }),
      links: links.map(function (l) { return { From: l[0], To: l[1], Count: l[2], Status: l[3], Why: l[4] }; })
    };
  }
  function synthetic() {
    var names = ["cmd/api", "cmd/worker", "cmd/admin", "api/http", "api/grpc", "api/webhooks", "auth", "sessions", "users",
      "orgs", "billing", "invoices", "payments", "pricing", "catalog", "search", "orders", "carts", "shipping", "inventory",
      "notify", "email", "sms", "audit", "reports", "exports", "imports", "jobs", "scheduler", "featureflags",
      "storage/sql", "storage/blob", "cache", "queue", "metrics", "logging", "config", "crypto", "clock", "util"];
    var tier = function (i) { return i < 3 ? 0 : i < 6 ? 1 : i < 30 ? 2 : 3; };
    var seed = 7, rnd = function () { seed = (seed * 16807) % 2147483647; return seed / 2147483647; };
    var links = [];
    names.forEach(function (a, i) {
      names.forEach(function (b, j) {
        if (tier(j) <= tier(i) || rnd() > (tier(i) === 2 && tier(j) === 2 ? 0.05 : 0.11)) return;
        var c = 1 + Math.floor(rnd() * rnd() * 30), r = rnd();
        links.push({ From: a, To: b, Count: c, Status: r < 0.12 ? "advisory" : r < 0.17 ? "accepted" : r < 0.4 ? "allowed" : "observed", Why: "Synthetic link." });
      });
    });
    [["users", "orders", 3], ["storage/sql", "billing", 2], ["util", "config", 1]].forEach(function (v) {
      links.push({ From: v[0], To: v[1], Count: v[2], Status: "violation", Why: "Synthetic violation: points against the dependency direction." });
    });
    return { nodes: names.map(function (n) { return { Name: n, Path: n + "/**" }; }), links: links };
  }

  // ---------- layout ----------
  function layout(nodes, links) {
    var out = {}, idx = {};
    nodes.forEach(function (n, i) { out[n.Name] = []; idx[n.Name] = i; });
    links.forEach(function (l) { if (out[l.From] && out[l.To] && l.From !== l.To) out[l.From].push(l); });
    // Break cycles: DFS from the modules with most outgoing imports; an edge back onto the stack does not rank.
    var state = {}, back = new Set();
    var order = nodes.map(function (n) { return n.Name; }).sort(function (a, b) { return out[b].length - out[a].length || (a < b ? -1 : 1); });
    function dfs(v) {
      state[v] = 1;
      out[v].slice().sort(function (a, b) { return b.Count - a.Count; }).forEach(function (l) {
        if (state[l.To] === 1) back.add(l);
        else if (!state[l.To]) dfs(l.To);
      });
      state[v] = 2;
    }
    order.forEach(function (v) { if (!state[v]) dfs(v); });
    // Rank = longest path from a module nothing imports.
    var rank = {}, indeg = {};
    nodes.forEach(function (n) { rank[n.Name] = 0; indeg[n.Name] = 0; });
    links.forEach(function (l) { if (!back.has(l) && idx[l.From] !== undefined && idx[l.To] !== undefined && l.From !== l.To) indeg[l.To]++; });
    var queue = order.filter(function (v) { return indeg[v] === 0; });
    while (queue.length) {
      var v = queue.shift();
      out[v].forEach(function (l) {
        if (back.has(l)) return;
        rank[l.To] = Math.max(rank[l.To], rank[v] + 1);
        if (--indeg[l.To] === 0) queue.push(l.To);
      });
    }
    var rows = [];
    nodes.forEach(function (n) { (rows[rank[n.Name]] = rows[rank[n.Name]] || []).push(n.Name); });
    rows = rows.filter(Boolean).map(function (r) { return r.sort(); });
    // Two barycenter sweeps reduce crossings; ties keep name order, so the picture is stable.
    var pos = {};
    function setPos() { rows.forEach(function (r) { r.forEach(function (v, i) { pos[v] = i - (r.length - 1) / 2; }); }); }
    setPos();
    for (var pass = 0; pass < 2; pass++) {
      rows.forEach(function (r, ri) {
        if (ri === 0) return;
        var bc = {};
        r.forEach(function (v) {
          var s = 0, n = 0;
          links.forEach(function (l) {
            if (l.To === v && pos[l.From] !== undefined && rank[l.From] < ri) { s += pos[l.From]; n++; }
            if (l.From === v && pos[l.To] !== undefined && rank[l.To] < ri) { s += pos[l.To]; n++; }
          });
          bc[v] = n ? s / n : pos[v];
        });
        r.sort(function (a, b) { return bc[a] - bc[b] || (a < b ? -1 : 1); });
      });
      setPos();
    }
    // A rank wider than MAX_ROW wraps onto further lines, so the drawing keeps a readable scale.
    var lines = [], lineRank = [];
    rows.forEach(function (r, ri) {
      for (var i = 0; i < r.length; i += MAX_ROW) { lines.push(r.slice(i, i + MAX_ROW)); lineRank.push(ri); }
    });
    rows = lines;
    var widest = Math.max.apply(null, rows.map(function (r) { return r.length; }));
    var width = PAD * 2 + widest * NODE_W + (widest - 1) * GAP_X;
    var xy = {};
    rows.forEach(function (r, ri) {
      var rowW = r.length * NODE_W + (r.length - 1) * GAP_X, x0 = (width - rowW) / 2;
      r.forEach(function (v, i) { xy[v] = { x: x0 + i * (NODE_W + GAP_X), y: PAD + ri * ROW_H, rank: lineRank[ri] }; });
    });
    return { xy: xy, width: width, height: PAD * 2 + (rows.length - 1) * ROW_H + NODE_H };
  }

  // ---------- drawing ----------
  function el(name, attrs, text) {
    var e = document.createElementNS(NS, name);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function weight(c) { return c >= 10 ? 4 : c >= 3 ? 2.5 : 1.5; }

  function Graph(host) {
    this.host = host;
    this.svgBox = host.querySelector("[data-g-canvas]");
    this.list = host.querySelector("[data-g-list]");
    this.detail = host.querySelector("[data-g-detail]");
    this.chips = host.querySelector("[data-g-chips]");
    this.matrixBox = host.querySelector("[data-g-matrix]");
    this.note = host.querySelector("[data-g-note]");
    this.show = { violation: true, advisory: true, accepted: true, allowed: true, observed: true };
    this.view = "graph"; this.scope = "auto"; this.sel = null;
  }
  Graph.prototype.load = function (data) {
    this.data = data; this.sel = null; this.scope = "auto";
    this.render();
  };
  Graph.prototype.visibleNodes = function () {
    var d = this.data, focus = this.scope === "problems" || (this.scope === "auto" && d.nodes.length > FOCUS_LIMIT);
    this.focused = focus;
    if (!focus) return d.nodes;
    var keep = {};
    d.links.forEach(function (l) { if (PROBLEM[l.Status]) { keep[l.From] = keep[l.To] = true; } });
    return d.nodes.filter(function (n) { return keep[n.Name]; });
  };
  Graph.prototype.render = function () {
    this.renderChips(); this.renderList();
    this.svgBox.hidden = this.view !== "graph"; this.matrixBox.hidden = this.view !== "matrix";
    if (this.view === "graph") this.renderGraph(); else this.renderMatrix();
    this.renderDetail();
  };
  Graph.prototype.counts = function () {
    var c = { violation: 0, advisory: 0, accepted: 0, allowed: 0, observed: 0 };
    this.data.links.forEach(function (l) { c[l.Status]++; });
    return c;
  };
  Graph.prototype.renderChips = function () {
    var self = this, c = this.counts();
    this.chips.innerHTML = "";
    ["violation", "advisory", "accepted", "allowed", "observed"].forEach(function (s) {
      if (!c[s]) return;
      var b = document.createElement("button");
      b.type = "button"; b.className = "gchip " + s; b.setAttribute("aria-pressed", String(self.show[s]));
      b.innerHTML = "<i></i><b>" + c[s] + "</b> " + LABEL[s];
      b.title = self.show[s] ? "Hide " + LABEL[s] : "Show " + LABEL[s];
      b.addEventListener("click", function () { self.show[s] = !self.show[s]; self.render(); });
      self.chips.appendChild(b);
    });
  };
  Graph.prototype.renderList = function () {
    var self = this;
    var problems = this.data.links.filter(function (l) { return PROBLEM[l.Status]; })
      .sort(function (a, b) { return RANK[a.Status] - RANK[b.Status] || b.Count - a.Count; });
    this.list.innerHTML = "";
    if (!problems.length) { this.list.innerHTML = "<li class='meta'>No violations or advisories.</li>"; return; }
    problems.forEach(function (l) {
      var li = document.createElement("li");
      li.tabIndex = 0;
      li.className = "gitem " + l.Status + (self.sel === l ? " sel" : "");
      li.innerHTML = "<i></i><span><code>" + l.From + "</code> → <code>" + l.To + "</code></span><span class='meta'>" + l.Count + "</span>";
      var pick = function () { self.sel = self.sel === l ? null : l; self.render(); };
      li.addEventListener("click", pick);
      li.addEventListener("keydown", function (e) { if (e.key === "Enter") pick(); });
      self.list.appendChild(li);
    });
  };
  Graph.prototype.renderGraph = function () {
    var self = this, nodes = this.visibleNodes(), names = {};
    nodes.forEach(function (n) { names[n.Name] = true; });
    // In focus mode fine links stay hidden: the picture is about problems.
    var links = this.data.links.filter(function (l) {
      return names[l.From] && names[l.To] && self.show[l.Status] && (!self.focused || PROBLEM[l.Status]);
    });
    var pair = {};
    links.forEach(function (l) { pair[l.From + "|" + l.To] = true; });
    var L = layout(nodes, links), xy = L.xy;
    var svg = el("svg", { viewBox: "0 0 " + L.width + " " + L.height, width: L.width, height: L.height, role: "img", "aria-label": "Module graph" });
    var defs = el("defs", {});
    ["violation", "advisory", "accepted", "allowed", "observed"].forEach(function (s) {
      var m = el("marker", { id: "gm-" + s, viewBox: "0 0 10 10", refX: 9, refY: 5, markerWidth: 9, markerHeight: 9, markerUnits: "userSpaceOnUse", orient: "auto-start-reverse" });
      m.appendChild(el("path", { d: "M0 0L10 5L0 10z", "class": "gm " + s }));
      defs.appendChild(m);
    });
    svg.appendChild(defs);
    var sel = this.sel, selNode = this.selNode;
    function related(l) { return (!sel && !selNode) || l === sel || (selNode && (l.From === selNode || l.To === selNode)); }
    // Fine links first, problems on top; within problems the biggest last.
    links.slice().sort(function (a, b) { return RANK[b.Status] - RANK[a.Status] || a.Count - b.Count; }).forEach(function (l) {
      var a = xy[l.From], b = xy[l.To];
      var down = b.y > a.y, x1 = a.x + NODE_W / 2, x2 = b.x + NODE_W / 2, d, mx, my;
      if (pair[l.To + "|" + l.From]) { var o = down ? -14 : 14; x1 += o; x2 += o; } // a cycle pair draws as two apart curves
      if (down) {
        var y1 = a.y + NODE_H, y2 = b.y - 2;
        d = "M" + x1 + " " + y1 + " C" + x1 + " " + (y1 + 40) + " " + x2 + " " + (y2 - 40) + " " + x2 + " " + y2;
        mx = (x1 + x2) / 2; my = (y1 + y2) / 2;
      } else { // upward or sideways: bow out to the side, so a pair in both directions draws as two curves
        var side = x2 >= x1 ? 1 : -1, ya = a.y, yb = b.y + NODE_H + 2;
        if (a.y === b.y) { ya = a.y + NODE_H / 2; yb = b.y + NODE_H / 2; x1 += side * NODE_W / 2; x2 -= side * NODE_W / 2; }
        var bow = 46;
        d = "M" + x1 + " " + ya + " C" + (x1 + side * bow) + " " + (ya - bow) + " " + (x2 + side * bow) + " " + (yb + bow) + " " + x2 + " " + yb;
        mx = (x1 + x2) / 2 + side * bow * 0.75; my = (ya + yb) / 2;
      }
      var g = el("g", { "class": "gedge " + l.Status + (related(l) ? "" : " dim") + (l === sel ? " sel" : ""), tabindex: PROBLEM[l.Status] ? 0 : -1 });
      g.appendChild(el("path", { d: d, "class": "hit" }));
      g.appendChild(el("path", { d: d, "class": "line", "stroke-width": weight(l.Count), "marker-end": "url(#gm-" + l.Status + ")" }));
      if (PROBLEM[l.Status] || l === sel) {
        var t = String(l.Count), w = 8 + t.length * 6;
        g.appendChild(el("rect", { x: mx - w / 2, y: my - 8, width: w, height: 16, rx: 8, "class": "badge" }));
        g.appendChild(el("text", { x: mx, y: my + 4, "text-anchor": "middle", "class": "badge-t" }, t));
      }
      g.appendChild(el("title", {}, l.From + " → " + l.To + " · " + l.Count + " imports · " + LABEL[l.Status]));
      g.addEventListener("click", function (e) { e.stopPropagation(); self.selNode = null; self.sel = self.sel === l ? null : l; self.render(); });
      g.addEventListener("keydown", function (e) { if (e.key === "Enter") { self.selNode = null; self.sel = l; self.render(); } });
      svg.appendChild(g);
    });
    var worst = {};
    this.data.links.forEach(function (l) {
      [l.From, l.To].forEach(function (n) { if (PROBLEM[l.Status] && (worst[n] === undefined || RANK[l.Status] < RANK[worst[n]])) worst[n] = l.Status; });
    });
    nodes.forEach(function (n) {
      var p = xy[n.Name], on = !sel && !selNode || selNode === n.Name || (sel && (sel.From === n.Name || sel.To === n.Name)) ||
        (selNode && links.some(function (l) { return (l.From === selNode && l.To === n.Name) || (l.To === selNode && l.From === n.Name); }));
      var g = el("g", { "class": "gnode" + (worst[n.Name] ? " " + worst[n.Name] : "") + (on ? "" : " dim") + (selNode === n.Name ? " sel" : ""), tabindex: 0 });
      g.appendChild(el("rect", { x: p.x, y: p.y, width: NODE_W, height: NODE_H, rx: 6 }));
      var name = n.Name.length > 17 ? n.Name.slice(0, 16) + "…" : n.Name;
      g.appendChild(el("text", { x: p.x + NODE_W / 2, y: p.y + 22, "text-anchor": "middle" }, name));
      g.appendChild(el("title", {}, n.Name + " · " + n.Path));
      var pick = function (e) { if (e) e.stopPropagation(); self.sel = null; self.selNode = self.selNode === n.Name ? null : n.Name; self.render(); };
      g.addEventListener("click", pick);
      g.addEventListener("keydown", function (e) { if (e.key === "Enter") pick(); });
      svg.appendChild(g);
    });
    svg.addEventListener("click", function () { if (self.sel || self.selNode) { self.sel = null; self.selNode = null; self.render(); } });
    this.svgBox.innerHTML = ""; this.svgBox.appendChild(svg);
    var total = this.data.nodes.length;
    this.note.innerHTML = this.focused
      ? "Showing the " + nodes.length + " of " + total + " modules that have a violation or advisory. <button type='button' class='linkish' data-g-all>Show all " + total + "</button> or use the matrix."
      : total > FOCUS_LIMIT ? total + " modules. <button type='button' class='linkish' data-g-focus>Problems only</button>" : "";
    var all = this.note.querySelector("[data-g-all]"), foc = this.note.querySelector("[data-g-focus]");
    if (all) all.addEventListener("click", function () { self.scope = "all"; self.render(); });
    if (foc) foc.addEventListener("click", function () { self.scope = "problems"; self.render(); });
  };
  Graph.prototype.renderMatrix = function () {
    var self = this, nodes = this.data.nodes, L = layout(nodes, this.data.links);
    var order = nodes.map(function (n) { return n.Name; }).sort(function (a, b) {
      return L.xy[a].rank - L.xy[b].rank || L.xy[a].x - L.xy[b].x;
    });
    var cell = {};
    this.data.links.forEach(function (l) { cell[l.From + "|" + l.To] = l; });
    var h = "<table class='gdsm'><thead><tr><th></th>";
    order.forEach(function (c) { h += "<th><span>" + c + "</span></th>"; });
    h += "</tr></thead><tbody>";
    order.forEach(function (r) {
      h += "<tr><th>" + r + "</th>";
      order.forEach(function (c) {
        var l = cell[r + "|" + c];
        if (r === c) h += "<td class='self'></td>";
        else if (!l || !self.show[l.Status]) h += "<td></td>";
        else h += "<td class='" + l.Status + "' title='" + r + " → " + c + " · " + l.Count + " · " + LABEL[l.Status] + "'>" + l.Count + "</td>";
      });
      h += "</tr>";
    });
    this.matrixBox.innerHTML = "<p class='meta'>Rows import columns. Ordered by dependency depth: a mark above the diagonal points up, against the usual direction.</p><div class='table-wrap'>" + h + "</tbody></table></div>";
    this.note.innerHTML = "";
  };
  Graph.prototype.renderDetail = function () {
    var d = this.detail, l = this.sel, n = this.selNode;
    if (l) {
      d.innerHTML = "<div class='row-between'><strong><code>" + l.From + "</code> → <code>" + l.To + "</code></strong><span class='chip " + l.Status + "'>" + LABEL[l.Status] + "</span></div>" +
        "<div class='meta'>" + l.Count + " imports</div><p style='margin-top:6px'>" + l.Why + "</p>";
    } else if (n) {
      var outs = this.data.links.filter(function (x) { return x.From === n; }), ins = this.data.links.filter(function (x) { return x.To === n; });
      var p = outs.concat(ins).filter(function (x) { return PROBLEM[x.Status]; }).length;
      d.innerHTML = "<strong><code>" + n + "</code></strong><div class='meta'>" + (this.data.nodes.find(function (x) { return x.Name === n; }).Path) +
        "</div><p style='margin-top:6px'>Imports " + outs.length + " modules, imported by " + ins.length + ". " + (p ? p + " with a problem." : "No problems.") + "</p>";
    } else {
      d.innerHTML = "<span class='meta'>Click a problem, an arrow or a module.</span>";
    }
  };

  // ---------- wiring ----------
  var host = document.getElementById("gproto");
  if (!host) return;
  var g = new Graph(host);
  var sets = { master: pilot(false), pr4: pilot(true), big: synthetic() };
  window.archfitGraph = function (key, view) {
    if (key) g.load(sets[key]);
    if (view) { g.view = view; g.render(); }
  };
  g.load(sets.master);
})();
