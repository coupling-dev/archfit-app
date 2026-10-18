// Prototype of the module graph: the policy status of module relationships in one report.
// Input follows the live View (Nodes{Name,Layer,PolicyLine}, Links{From,To,Count,Status}, Stale, Incomplete) plus a
// per-link finding list for the detail panel, which the live page would need as a new projection.
// Rules kept from review: Status comes from graph.Build and is never recomputed here; every report string goes in
// through textContent or setAttribute; module names are keys of Maps, pairs keyed collision-free.
(function () {
  "use strict";
  var NS = "http://www.w3.org/2000/svg";
  var NODE_W = 132, NODE_H = 34, GAP_X = 28, ROW_H = 96, PAD = 24, BAND = 22;
  var MAX_ROW = 6;      // modules per drawn line
  var FOCUS_LIMIT = 15; // above this many modules the graph starts on problem links
  var STATUSES = ["violation", "accepted", "advisory", "allowed", "observed"]; // graph.Build precedence
  var TRIAGE = { violation: 0, advisory: 1, accepted: 2, allowed: 3, observed: 4 };
  var PROBLEM = { violation: true, advisory: true, accepted: true };
  var LABEL = { violation: "violation", accepted: "accepted (baseline or waiver)", advisory: "advisory", allowed: "allowed", observed: "observed" };
  var MEANING = {
    violation: "An active gate finding names this pair.",
    accepted: "A gate finding names this pair, accepted by the baseline or a waiver.",
    advisory: "A finding that does not block names this pair.",
    allowed: "A blocking allowlist or layer rule permits this pair.",
    observed: "No active finding names this pair and no rule permits it. It can still break a rule the report did not evaluate."
  };

  // ---------- data ----------
  var gateFinding = { rule: "no-module-cycles", kind: "gate", status: "new" };
  var coupling = { rule: "bc/imbalanced_coupling", kind: "advisory", status: "new" };
  function pilot(pr) {
    var f = function (base, why) { return [{ rule: base.rule, kind: base.kind, status: base.status, why: why }]; };
    var links = [
      ["cmd", "pkg_chaos", 8, "advisory", f(coupling, "Functional coupling across a module boundary to a module of undeclared volatility.")],
      ["cmd", "pkg_runtime", 3, "advisory", f(coupling, "Functional coupling across a module boundary to a module of undeclared volatility.")],
      ["cmd", "pkg_container", 2, "observed", []],
      ["pkg_chaos", "pkg_container", 40, pr ? "violation" : "advisory",
        pr ? f(gateFinding, "pkg_chaos depends on pkg_container, and the two are in a dependency cycle.").concat(f(coupling, "Functional coupling to a module of undeclared volatility."))
          : f(coupling, "Functional coupling to a module of undeclared volatility.")],
      ["pkg_chaos", "pkg_util", 2, "advisory", f(coupling, "Functional coupling to a module of undeclared volatility.")],
      ["pkg_runtime", "pkg_container", 20, "advisory", f(coupling, "Functional coupling to a module of undeclared volatility.")],
      ["pkg_runtime", "pkg_tc", 2, "advisory", f(coupling, "Functional coupling to a module of undeclared volatility.")]
    ];
    if (pr) links.push(["pkg_container", "pkg_chaos", 1, "violation", f(gateFinding, "pkg_container depends on pkg_chaos, and the two are in a dependency cycle.")]);
    return {
      Incomplete: pr, // PR #4: decision.hard_gates = fail
      Nodes: ["cmd", "pkg_chaos", "pkg_container", "pkg_runtime", "pkg_tc", "pkg_util"].map(function (n, i) { return { Name: n, Layer: "", PolicyLine: 40 + i * 3 }; }),
      Links: links.map(function (l) { return { From: l[0], To: l[1], Count: l[2], Status: l[3], Findings: l[4] }; })
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
        var s = r < 0.12 ? "advisory" : r < 0.17 ? "accepted" : r < 0.4 ? "allowed" : "observed";
        links.push({ From: a, To: b, Count: c, Status: s, Findings: PROBLEM[s] ? [{ rule: "synthetic", kind: s === "advisory" ? "advisory" : "gate", status: s === "accepted" ? "baseline" : "new", why: "Synthetic finding." }] : [] });
      });
    });
    [["users", "orders", 3], ["storage/sql", "billing", 2], ["util", "config", 1]].forEach(function (v) {
      links.push({ From: v[0], To: v[1], Count: v[2], Status: "violation", Findings: [{ rule: "synthetic-direction", kind: "gate", status: "new", why: "Synthetic violation." }] });
    });
    links.push({ From: "jobs", To: "jobs", Count: 4, Status: "advisory", Findings: [{ rule: "synthetic", kind: "advisory", status: "new", why: "Synthetic finding inside one module." }] });
    var nodes = names.map(function (n, i) { return { Name: n, Layer: "", PolicyLine: 10 + i }; });
    nodes.push({ Name: "legacy <b>tools</b>", Layer: "", PolicyLine: 0 }); // not in the policy; HTML in a name stays text
    links.push({ From: "legacy <b>tools</b>", To: "util", Count: 5, Status: "observed", Findings: [] });
    return { Incomplete: false, Nodes: nodes, Links: links };
  }

  // ---------- layout: computed once per dataset and scope, never per filter ----------
  function key(a, b) { return JSON.stringify([a, b]); }
  function layout(nodes, links) {
    var names = nodes.map(function (n) { return n.Name; }), has = new Map(names.map(function (n) { return [n, true]; }));
    var out = new Map(names.map(function (n) { return [n, []]; }));
    links.forEach(function (l) { if (has.get(l.From) && has.get(l.To) && l.From !== l.To) out.get(l.From).push(l); });
    var layered = nodes.some(function (n) { return n.Layer; });
    var rows, bands = [];
    if (layered) {
      // Declared layers group the modules in their policy order; names alone give no hierarchy.
      var byLayer = new Map();
      nodes.forEach(function (n) { var k = n.Layer || "(no layer)"; if (!byLayer.has(k)) byLayer.set(k, []); byLayer.get(k).push(n.Name); });
      rows = []; byLayer.forEach(function (v, k) { rows.push(v.sort()); bands.push(k); });
    } else {
      // No layers: depth after a cycle break places importers above. A layout only, never a permitted direction.
      var state = new Map(), back = new Set();
      var order = names.slice().sort(function (a, b) { return out.get(b).length - out.get(a).length || (a < b ? -1 : 1); });
      var dfs = function (v) {
        state.set(v, 1);
        out.get(v).slice().sort(function (a, b) { return b.Count - a.Count; }).forEach(function (l) {
          if (state.get(l.To) === 1) back.add(l); else if (!state.get(l.To)) dfs(l.To);
        });
        state.set(v, 2);
      };
      order.forEach(function (v) { if (!state.get(v)) dfs(v); });
      var rank = new Map(), indeg = new Map();
      names.forEach(function (n) { rank.set(n, 0); indeg.set(n, 0); });
      out.forEach(function (ls) { ls.forEach(function (l) { if (!back.has(l)) indeg.set(l.To, indeg.get(l.To) + 1); }); });
      var queue = order.filter(function (v) { return indeg.get(v) === 0; });
      while (queue.length) {
        var v = queue.shift();
        out.get(v).forEach(function (l) {
          if (back.has(l)) return;
          rank.set(l.To, Math.max(rank.get(l.To), rank.get(v) + 1));
          indeg.set(l.To, indeg.get(l.To) - 1);
          if (indeg.get(l.To) === 0) queue.push(l.To);
        });
      }
      rows = [];
      names.forEach(function (n) { var r = rank.get(n); (rows[r] = rows[r] || []).push(n); });
      rows = rows.filter(Boolean).map(function (r) { return r.sort(); });
      var pos = new Map(), setPos = function () { rows.forEach(function (r) { r.forEach(function (v, i) { pos.set(v, i - (r.length - 1) / 2); }); }); };
      setPos();
      for (var pass = 0; pass < 2; pass++) {
        rows.forEach(function (r, ri) {
          if (ri === 0) return;
          var bc = new Map();
          r.forEach(function (v) {
            var s = 0, n = 0;
            links.forEach(function (l) {
              if (l.To === v && rank.get(l.From) < ri) { s += pos.get(l.From); n++; }
              if (l.From === v && rank.get(l.To) < ri) { s += pos.get(l.To); n++; }
            });
            bc.set(v, n ? s / n : pos.get(v));
          });
          r.sort(function (a, b) { return bc.get(a) - bc.get(b) || (a < b ? -1 : 1); });
        });
        setPos();
      }
    }
    var lines = [], lineBand = [];
    rows.forEach(function (r, ri) {
      for (var i = 0; i < r.length; i += MAX_ROW) { lines.push(r.slice(i, i + MAX_ROW)); lineBand.push(i === 0 && layered ? bands[ri] : ""); }
    });
    var widest = Math.max(1, Math.max.apply(null, lines.map(function (r) { return r.length; })));
    var width = PAD * 2 + widest * NODE_W + (widest - 1) * GAP_X, top = layered ? BAND : 0;
    var xy = new Map(), labels = [], y = PAD;
    lines.forEach(function (r, li) {
      if (lineBand[li]) { labels.push({ text: lineBand[li], y: y }); y += top; }
      var rowW = r.length * NODE_W + (r.length - 1) * GAP_X, x0 = (width - rowW) / 2;
      r.forEach(function (v, i) { xy.set(v, { x: x0 + i * (NODE_W + GAP_X), y: y, line: li }); });
      y += ROW_H;
    });
    return { xy: xy, width: width, height: y - ROW_H + NODE_H + PAD + 12, labels: labels, layered: layered };
  }

  // ---------- DOM helpers: report strings only ever go through textContent and setAttribute ----------
  function svgEl(name, attrs, text) {
    var e = document.createElementNS(NS, name);
    Object.keys(attrs).forEach(function (k) { e.setAttribute(k, attrs[k]); });
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function h(tag, attrs, children) {
    var e = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (k) { if (k === "text") e.textContent = attrs[k]; else e.setAttribute(k, attrs[k]); });
    (children || []).forEach(function (c) { if (c) e.appendChild(typeof c === "string" ? document.createTextNode(c) : c); });
    return e;
  }
  function pairEl(l) { return h("span", {}, [h("code", { text: l.From }), " → ", h("code", { text: l.To })]); }
  function weight(c) { return c >= 10 ? 4 : c >= 3 ? 2.5 : 1.5; }

  // ---------- component ----------
  function Graph(root) {
    var q = function (s) { return root.querySelector(s); };
    this.canvas = q("[data-g-canvas]"); this.list = q("[data-g-list]"); this.detail = q("[data-g-detail]");
    this.chips = q("[data-g-chips]"); this.matrixBox = q("[data-g-matrix]"); this.note = q("[data-g-note]");
    this.banner = q("[data-g-banner]");
    this.view = "graph";
  }
  Graph.prototype.load = function (data) {
    this.data = data; this.sel = null; this.selNode = null; this.scope = "auto"; this.layouts = new Map();
    this.show = { violation: true, accepted: true, advisory: true, allowed: true, observed: true };
    this.render();
  };
  Graph.prototype.focused = function () {
    var d = this.data, hasProblem = d.Links.some(function (l) { return PROBLEM[l.Status]; });
    return hasProblem && (this.scope === "problems" || (this.scope === "auto" && d.Nodes.length > FOCUS_LIMIT));
  };
  // One visibility rule for the list, the graph and the matrix.
  Graph.prototype.visible = function (l) {
    return this.show[l.Status] && (!this.focused() || PROBLEM[l.Status]);
  };
  Graph.prototype.layoutFor = function (focus) {
    var k = focus ? "focus" : "all";
    if (!this.layouts.has(k)) {
      var d = this.data, nodes = d.Nodes, links = d.Links;
      if (focus) {
        var keep = new Map();
        links.forEach(function (l) { if (PROBLEM[l.Status]) { keep.set(l.From, true); keep.set(l.To, true); } });
        nodes = nodes.filter(function (n) { return keep.get(n.Name); });
        links = links.filter(function (l) { return PROBLEM[l.Status]; });
      }
      this.layouts.set(k, { nodes: nodes, L: layout(nodes, links) });
    }
    return this.layouts.get(k);
  };
  Graph.prototype.render = function () {
    this.banner.hidden = !this.data.Incomplete;
    if (!this.data.Nodes.length) {
      this.chips.replaceChildren(); this.list.replaceChildren(); this.matrixBox.hidden = true;
      this.canvas.hidden = false;
      this.canvas.replaceChildren(h("div", { "class": "blankslate" }, [h("h3", { text: "No modules in this report" }), h("p", { text: "Declare modules in .archfit.yaml, or open the report on the archfit/state check." })]));
      this.note.replaceChildren(); this.detail.replaceChildren();
      return;
    }
    this.renderChips(); this.renderList();
    this.canvas.hidden = this.view !== "graph"; this.matrixBox.hidden = this.view !== "matrix";
    if (this.view === "graph") this.renderGraph(); else this.renderMatrix();
    this.renderNote(); this.renderDetail();
  };
  Graph.prototype.renderChips = function () {
    var self = this, c = {};
    this.data.Links.forEach(function (l) { c[l.Status] = (c[l.Status] || 0) + 1; });
    this.chips.replaceChildren();
    STATUSES.slice().sort(function (a, b) { return TRIAGE[a] - TRIAGE[b]; }).forEach(function (s) {
      if (!c[s]) return;
      var b = h("button", { type: "button", "class": "gchip " + s, "aria-pressed": String(self.show[s]), title: MEANING[s] },
        [h("i"), h("b", { text: String(c[s]) }), " " + LABEL[s]]);
      b.addEventListener("click", function () {
        self.show[s] = !self.show[s];
        if (!PROBLEM[s] && self.show[s] && self.focused()) self.scope = "all"; // asking for fine links means the full picture
        self.render();
      });
      self.chips.appendChild(b);
    });
  };
  Graph.prototype.renderList = function () {
    var self = this;
    var problems = this.data.Links.filter(function (l) { return PROBLEM[l.Status] && self.show[l.Status]; })
      .sort(function (a, b) { return TRIAGE[a.Status] - TRIAGE[b.Status] || b.Count - a.Count; });
    this.list.replaceChildren();
    if (!problems.length) { this.list.appendChild(h("li", { "class": "meta", style: "padding:6px 16px", text: "No violations, advisories or accepted exceptions shown." })); return; }
    problems.forEach(function (l) {
      var li = h("li", { tabindex: "0", "class": "gitem " + l.Status + (self.sel === l ? " sel" : "") }, [h("i"), pairEl(l), h("span", { "class": "meta", text: String(l.Count) })]);
      var pick = function () { self.selNode = null; self.sel = self.sel === l ? null : l; self.render(); };
      li.addEventListener("click", pick);
      li.addEventListener("keydown", function (e) { if (e.key === "Enter") pick(); });
      self.list.appendChild(li);
    });
  };
  Graph.prototype.renderGraph = function () {
    var self = this, focus = this.focused(), lay = this.layoutFor(focus), L = lay.L, xy = L.xy;
    var links = this.data.Links.filter(function (l) { return xy.has(l.From) && xy.has(l.To) && self.visible(l); });
    var pairs = new Set(links.map(function (l) { return key(l.From, l.To); }));
    var svg = svgEl("svg", { viewBox: "0 0 " + L.width + " " + L.height, width: L.width, height: L.height, role: "img", "aria-label": "Module relationships by policy status" });
    var defs = svgEl("defs", {});
    STATUSES.forEach(function (s) {
      var m = svgEl("marker", { id: "gm-" + s, viewBox: "0 0 10 10", refX: 9, refY: 5, markerWidth: 9, markerHeight: 9, markerUnits: "userSpaceOnUse", orient: "auto-start-reverse" });
      m.appendChild(svgEl("path", { d: "M0 0L10 5L0 10z", "class": "gm " + s }));
      defs.appendChild(m);
    });
    svg.appendChild(defs);
    L.labels.forEach(function (b) { svg.appendChild(svgEl("text", { x: PAD, y: b.y + 12, "class": "gband" }, b.text)); });
    var sel = this.sel, selNode = this.selNode;
    var related = function (l) { return (!sel && selNode === null) || l === sel || (selNode !== null && (l.From === selNode || l.To === selNode)); };
    links.slice().sort(function (a, b) { return TRIAGE[b.Status] - TRIAGE[a.Status] || a.Count - b.Count; }).forEach(function (l) {
      var a = xy.get(l.From), b = xy.get(l.To), d, mx, my;
      if (l.From === l.To) { // a self-link: a small loop on the right side of the module
        var rx = a.x + NODE_W, ry = a.y + NODE_H / 2;
        d = "M" + rx + " " + (ry - 8) + " C" + (rx + 34) + " " + (ry - 30) + " " + (rx + 34) + " " + (ry + 30) + " " + (rx + 2) + " " + (ry + 8);
        mx = rx + 26; my = ry;
      } else {
        var down = b.y > a.y, x1 = a.x + NODE_W / 2, x2 = b.x + NODE_W / 2;
        if (pairs.has(key(l.To, l.From))) { var o = down ? -14 : 14; x1 += o; x2 += o; }
        if (down) {
          var y1 = a.y + NODE_H, y2 = b.y - 2;
          d = "M" + x1 + " " + y1 + " C" + x1 + " " + (y1 + 40) + " " + x2 + " " + (y2 - 40) + " " + x2 + " " + y2;
          mx = (x1 + x2) / 2; my = (y1 + y2) / 2;
        } else {
          var side = x2 >= x1 ? 1 : -1, ya = a.y, yb = b.y + NODE_H + 2, bow = 46;
          if (a.y === b.y) { ya = a.y + NODE_H / 2; yb = b.y + NODE_H / 2; x1 += side * NODE_W / 2; x2 -= side * NODE_W / 2; }
          d = "M" + x1 + " " + ya + " C" + (x1 + side * bow) + " " + (ya - bow) + " " + (x2 + side * bow) + " " + (yb + bow) + " " + x2 + " " + yb;
          mx = (x1 + x2) / 2 + side * bow * 0.75; my = (ya + yb) / 2;
        }
      }
      var g = svgEl("g", { "class": "gedge " + l.Status + (related(l) ? "" : " dim") + (l === sel ? " sel" : ""), tabindex: PROBLEM[l.Status] ? 0 : -1 });
      g.appendChild(svgEl("path", { d: d, "class": "hit" }));
      g.appendChild(svgEl("path", { d: d, "class": "line", "stroke-width": weight(l.Count), "marker-end": "url(#gm-" + l.Status + ")" }));
      if (PROBLEM[l.Status] || l === sel) {
        var t = String(l.Count), w = 8 + t.length * 6;
        g.appendChild(svgEl("rect", { x: mx - w / 2, y: my - 8, width: w, height: 16, rx: 8, "class": "badge" }));
        g.appendChild(svgEl("text", { x: mx, y: my + 4, "text-anchor": "middle", "class": "badge-t" }, t));
      }
      g.appendChild(svgEl("title", {}, l.From + " → " + l.To + " · " + l.Count + " observed dependencies · " + LABEL[l.Status]));
      var pick = function (e) { if (e) e.stopPropagation(); self.selNode = null; self.sel = self.sel === l ? null : l; self.render(); };
      g.addEventListener("click", pick);
      g.addEventListener("keydown", function (e) { if (e.key === "Enter") pick(); });
      svg.appendChild(g);
    });
    var worst = new Map();
    this.data.Links.forEach(function (l) {
      if (!PROBLEM[l.Status] || !self.show[l.Status]) return;
      [l.From, l.To].forEach(function (n) { if (!worst.has(n) || TRIAGE[l.Status] < TRIAGE[worst.get(n)]) worst.set(n, l.Status); });
    });
    lay.nodes.forEach(function (n) {
      var p = xy.get(n.Name);
      var on = (!sel && selNode === null) || selNode === n.Name || (sel && (sel.From === n.Name || sel.To === n.Name)) ||
        (selNode !== null && links.some(function (l) { return (l.From === selNode && l.To === n.Name) || (l.To === selNode && l.From === n.Name); }));
      var g = svgEl("g", { "class": "gnode" + (worst.has(n.Name) ? " " + worst.get(n.Name) : "") + (n.PolicyLine ? "" : " undeclared") + (on ? "" : " dim") + (selNode === n.Name ? " sel" : ""), tabindex: 0 });
      g.appendChild(svgEl("rect", { x: p.x, y: p.y, width: NODE_W, height: NODE_H, rx: 6 }));
      g.appendChild(svgEl("text", { x: p.x + NODE_W / 2, y: p.y + 22, "text-anchor": "middle" }, n.Name.length > 17 ? n.Name.slice(0, 16) + "…" : n.Name));
      g.appendChild(svgEl("title", {}, n.Name + (n.PolicyLine ? "" : " · not in the policy")));
      var pick = function (e) { if (e) e.stopPropagation(); self.sel = null; self.selNode = self.selNode === n.Name ? null : n.Name; self.render(); };
      g.addEventListener("click", pick);
      g.addEventListener("keydown", function (e) { if (e.key === "Enter") pick(); });
      svg.appendChild(g);
    });
    svg.addEventListener("click", function () { if (self.sel || self.selNode !== null) { self.sel = null; self.selNode = null; self.render(); } });
    this.canvas.replaceChildren(svg);
  };
  Graph.prototype.renderNote = function () {
    var self = this, total = this.data.Nodes.length, note = this.note;
    note.replaceChildren();
    if (this.view !== "graph") return;
    var btn = function (label, scope) {
      var b = h("button", { type: "button", "class": "linkish", text: label });
      b.addEventListener("click", function () { self.scope = scope; self.sel = null; self.selNode = null; self.render(); });
      return b;
    };
    if (this.focused()) {
      note.append("Showing the " + this.layoutFor(true).nodes.length + " of " + total + " modules with a violation, advisory or accepted exception. ", btn("Show all " + total, "all"), " or use the matrix.");
    } else if (total > FOCUS_LIMIT && this.data.Links.some(function (l) { return PROBLEM[l.Status]; })) {
      note.append(total + " modules. ", btn("Problems only", "problems"));
    }
    var L = this.layoutFor(this.focused()).L;
    note.append(L.layered ? " Grouped by declared layer." : " No layers declared: depth places importers above; position does not establish permission.");
  };
  Graph.prototype.renderMatrix = function () {
    var self = this, L = this.layoutFor(false).L;
    var order = this.data.Nodes.map(function (n) { return n.Name; }).sort(function (a, b) {
      var p = L.xy.get(a), q = L.xy.get(b); return p.line - q.line || p.x - q.x;
    });
    var cell = new Map();
    this.data.Links.forEach(function (l) { cell.set(key(l.From, l.To), l); });
    var table = h("table", { "class": "gdsm" }), head = h("tr", {}, [h("th")]);
    order.forEach(function (c) { head.appendChild(h("th", {}, [h("span", { text: c })])); });
    table.appendChild(h("thead", {}, [head]));
    var body = h("tbody");
    order.forEach(function (r) {
      var tr = h("tr", {}, [h("th", { text: r })]);
      order.forEach(function (c) {
        var l = cell.get(key(r, c)), td = h("td", { "class": r === c ? "self" : "" });
        if (l && self.visible(l)) {
          td.className = l.Status + (r === c ? " selfl" : "") + (self.sel === l ? " sel" : "");
          td.textContent = String(l.Count);
          td.title = r + " → " + c + " · " + l.Count + " · " + LABEL[l.Status];
          td.tabIndex = 0;
          td.addEventListener("click", function () { self.selNode = null; self.sel = self.sel === l ? null : l; self.render(); });
        }
        tr.appendChild(td);
      });
      body.appendChild(tr);
    });
    table.appendChild(body);
    this.matrixBox.replaceChildren(h("p", { "class": "meta", text: "Rows depend on columns. Position does not establish permission." }), h("div", { "class": "table-wrap" }, [table]));
  };
  Graph.prototype.renderDetail = function () {
    var d = this.detail, l = this.sel, n = this.selNode;
    d.replaceChildren();
    if (l) {
      d.append(h("div", { "class": "row-between" }, [h("strong", {}, [pairEl(l)]), h("span", { "class": "chip " + l.Status, text: LABEL[l.Status] })]),
        h("div", { "class": "meta", text: l.Count + " observed dependencies" }),
        h("p", { "class": "meta", style: "margin-top:6px", text: MEANING[l.Status] }));
      (l.Findings || []).forEach(function (f) {
        d.append(h("p", { style: "margin-top:6px" }, [h("code", { text: f.rule }), h("span", { "class": "meta", text: " · " + f.kind + ", " + f.status }), h("br"), f.why]));
      });
    } else if (n !== null) {
      var node = this.data.Nodes.find(function (x) { return x.Name === n; });
      var outs = this.data.Links.filter(function (x) { return x.From === n && x.To !== n; }), ins = this.data.Links.filter(function (x) { return x.To === n && x.From !== n; });
      var p = this.data.Links.filter(function (x) { return (x.From === n || x.To === n) && PROBLEM[x.Status]; }).length;
      d.append(h("strong", {}, [h("code", { text: n })]),
        h("div", { "class": "meta", text: node && node.PolicyLine ? "Policy line " + node.PolicyLine : "Not in the policy" }),
        h("p", { style: "margin-top:6px", text: "Depends on " + outs.length + ", used by " + ins.length + ". " + (p ? p + " with a problem." : "No problems.") }));
    } else {
      d.append(h("span", { "class": "meta", text: "Click a problem, an arrow, a cell or a module." }));
    }
  };

  // ---------- wiring ----------
  var root = document.getElementById("gproto");
  if (!root) return;
  var g = new Graph(root);
  var sets = { master: pilot(false), pr4: pilot(true), big: synthetic(), empty: { Incomplete: false, Nodes: [], Links: [] } };
  window.archfitGraph = function (k, view) {
    if (view) g.view = view;
    if (k && sets[k]) g.load(sets[k]); else if (view) g.render();
  };
  g.load(sets.master);
})();
