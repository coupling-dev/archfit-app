// Draws the policy status of module relationships from the embedded View: problem links first, a deterministic
// layout, and a matrix for the full picture. Report strings only ever go in through textContent and setAttribute;
// module names are Map keys and pairs are keyed collision-free. The server-rendered tables stay the fallback.
(function () {
  "use strict";
  var NS = "http://www.w3.org/2000/svg";
  var NODE_W = 132,
    NODE_H = 34,
    GAP_X = 28,
    ROW_H = 96,
    PAD = 24,
    BAND = 22;
  var MAX_ROW = 6; // modules per drawn line
  var ARC = 40; // the highest a same-line arc may rise: below the gap between lines and the reserved margins
  var FOCUS_LIMIT = 15; // above this many modules the graph starts on problem links
  var MAX_DRAWN = 300; // above this the tables are the view
  var STATUSES = ["violation", "accepted", "advisory", "allowed", "observed"]; // graph.Build precedence
  var TRIAGE = { violation: 0, advisory: 1, accepted: 2, allowed: 3, observed: 4 };
  var PROBLEM = { violation: true, advisory: true, accepted: true };
  var LABEL = {
    violation: "violation",
    accepted: "accepted (baseline or waiver)",
    advisory: "advisory",
    allowed: "allowed",
    observed: "observed",
  };
  var MEANING = {
    violation:
      "An active gate finding, or a gate finding of a status this version does not know, names this pair.",
    accepted: "A gate finding names this pair, accepted by the baseline or a waiver.",
    advisory: "A finding that does not block names this pair.",
    allowed: "A blocking allowlist or layer rule permits this pair.",
    observed:
      "No active finding names this pair. That is not proof that a rule permits it, and it can still break a rule the report did not evaluate.",
  };

  var data = document.getElementById("graph-data");
  var root = document.getElementById("graph");
  if (!data || !root) return;
  var view = JSON.parse(data.textContent);
  var nodes = view.Nodes || [],
    links = view.Links || [];
  var policyUrl = root.dataset.policyUrl;
  // explain maps a link's pair to the findings that name it (View.Findings), in report order.
  var explain = new Map();
  (view.Findings || []).forEach(function (f) {
    var k = key(f.From, f.To);
    if (!explain.has(k)) explain.set(k, []);
    explain.get(k).push(f);
  });

  function key(a, b) {
    return JSON.stringify([a, b]);
  }
  function svgEl(name, attrs, text) {
    var e = document.createElementNS(NS, name);
    Object.keys(attrs).forEach(function (k) {
      e.setAttribute(k, attrs[k]);
    });
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function h(tag, attrs, children) {
    var e = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (k) {
      if (k === "text") e.textContent = attrs[k];
      else e.setAttribute(k, attrs[k]);
    });
    (children || []).forEach(function (c) {
      if (c) e.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    });
    return e;
  }
  function pairEl(l) {
    return h("span", {}, [h("code", { text: l.From }), " → ", h("code", { text: l.To })]);
  }
  function weight(c) {
    return c >= 10 ? 4 : c >= 3 ? 2.5 : 1.5;
  }
  function count(l) {
    return l.Count ? String(l.Count) : "finding";
  }

  // Layout: declared layers make bands in policy order; without layers, depth after a cycle break places importers
  // above. Depth is a layout only, never a permitted direction. Ties by name, so a report always draws the same.
  function layout(ns, ls) {
    var names = ns.map(function (n) {
        return n.Name;
      }),
      has = new Map(
        names.map(function (n) {
          return [n, true];
        }),
      );
    var out = new Map(
      names.map(function (n) {
        return [n, []];
      }),
    );
    ls.forEach(function (l) {
      if (has.get(l.From) && has.get(l.To) && l.From !== l.To) out.get(l.From).push(l);
    });
    var layered = ns.some(function (n) {
      return n.Layer;
    });
    var rows = [],
      bands = [];
    if (layered) {
      var byLayer = new Map();
      ns.forEach(function (n) {
        var k = n.Layer || "(no layer)";
        if (!byLayer.has(k)) byLayer.set(k, []);
        byLayer.get(k).push(n.Name);
      });
      // Bands follow the policy: a layer's position is its first module's policy line; "(no layer)" goes last.
      var first = function (k) {
        return Math.min.apply(
          null,
          ns
            .filter(function (n) {
              return (n.Layer || "(no layer)") === k;
            })
            .map(function (n) {
              return n.Layer && n.PolicyLine ? n.PolicyLine : Infinity;
            }),
        );
      };
      Array.from(byLayer.keys())
        .sort(function (a, b) {
          return first(a) - first(b) || (a < b ? -1 : 1);
        })
        .forEach(function (k) {
          rows.push(byLayer.get(k).sort());
          bands.push(k);
        });
    } else {
      var state = new Map(),
        back = new Set();
      var order = names.slice().sort(function (a, b) {
        return out.get(b).length - out.get(a).length || (a < b ? -1 : 1);
      });
      var dfs = function (v) {
        state.set(v, 1);
        out
          .get(v)
          .slice()
          .sort(function (a, b) {
            return b.Count - a.Count;
          })
          .forEach(function (l) {
            if (state.get(l.To) === 1) back.add(l);
            else if (!state.get(l.To)) dfs(l.To);
          });
        state.set(v, 2);
      };
      order.forEach(function (v) {
        if (!state.get(v)) dfs(v);
      });
      var rank = new Map(),
        indeg = new Map();
      names.forEach(function (n) {
        rank.set(n, 0);
        indeg.set(n, 0);
      });
      out.forEach(function (o) {
        o.forEach(function (l) {
          if (!back.has(l)) indeg.set(l.To, indeg.get(l.To) + 1);
        });
      });
      var queue = order.filter(function (v) {
        return indeg.get(v) === 0;
      });
      while (queue.length) {
        var v = queue.shift();
        out.get(v).forEach(function (l) {
          if (back.has(l)) return;
          rank.set(l.To, Math.max(rank.get(l.To), rank.get(v) + 1));
          indeg.set(l.To, indeg.get(l.To) - 1);
          if (indeg.get(l.To) === 0) queue.push(l.To);
        });
      }
      names.forEach(function (n) {
        var r = rank.get(n);
        (rows[r] = rows[r] || []).push(n);
      });
      rows = rows.filter(Boolean).map(function (r) {
        return r.sort();
      });
      var pos = new Map(),
        setPos = function () {
          rows.forEach(function (r) {
            r.forEach(function (n, i) {
              pos.set(n, i - (r.length - 1) / 2);
            });
          });
        };
      setPos();
      for (var pass = 0; pass < 2; pass++) {
        // barycenter passes reduce crossings
        rows.forEach(function (r, ri) {
          if (ri === 0) return;
          var bc = new Map();
          r.forEach(function (n) {
            var s = 0,
              k = 0;
            ls.forEach(function (l) {
              if (l.To === n && rank.get(l.From) < ri) {
                s += pos.get(l.From);
                k++;
              }
              if (l.From === n && rank.get(l.To) < ri) {
                s += pos.get(l.To);
                k++;
              }
            });
            bc.set(n, k ? s / k : pos.get(n));
          });
          r.sort(function (a, b) {
            return bc.get(a) - bc.get(b) || (a < b ? -1 : 1);
          });
        });
        setPos();
      }
    }
    var lines = [],
      lineBand = [];
    rows.forEach(function (r, ri) {
      for (var i = 0; i < r.length; i += MAX_ROW) {
        lines.push(r.slice(i, i + MAX_ROW));
        lineBand.push(i === 0 && layered ? bands[ri] : "");
      }
    });
    var widest = Math.max(
      1,
      Math.max.apply(
        null,
        lines.map(function (r) {
          return r.length;
        }),
      ),
    );
    var width = PAD * 2 + widest * NODE_W + (widest - 1) * GAP_X;
    var xy = new Map(),
      labels = [],
      y = PAD + ARC;
    lines.forEach(function (r, li) {
      if (lineBand[li]) {
        labels.push({ text: lineBand[li], y: y });
        y += BAND;
      }
      var x0 = (width - (r.length * NODE_W + (r.length - 1) * GAP_X)) / 2;
      r.forEach(function (n, i) {
        xy.set(n, { x: x0 + i * (NODE_W + GAP_X), y: y, line: li });
      });
      y += ROW_H;
    });
    return {
      xy: xy,
      width: width,
      height: y - ROW_H + NODE_H + ARC + PAD,
      labels: labels,
      layered: layered,
    };
  }

  // ---------- state ----------
  var q = function (id) {
    return document.getElementById(id);
  };
  var chips = q("g-chips"),
    list = q("g-list"),
    detail = q("g-detail"),
    canvas = q("g-canvas"),
    matrixBox = q("g-matrix"),
    note = q("g-note");
  var tables = q("graph-tables");
  if (!chips || nodes.length === 0) return; // the tables stay open as the view
  if (nodes.length > MAX_DRAWN) {
    filterable();
    return;
  }
  if (tables) tables.open = false;
  root.hidden = false;
  var show = { violation: true, accepted: true, advisory: true, allowed: true, observed: true };
  var scope = "auto",
    mode = "graph",
    sel = null,
    selNode = null,
    layouts = new Map();
  var hasProblem = links.some(function (l) {
    return PROBLEM[l.Status];
  });

  // Above MAX_DRAWN the tables are the view, with a filter over their rows.
  function filterable() {
    if (!tables) return;
    var input = h("input", { type: "search", placeholder: "Filter modules and links", "aria-label": "Filter modules and links" });
    input.addEventListener("input", function () {
      var v = input.value.toLowerCase();
      tables.querySelectorAll("#graph-nodes tbody tr, #graph-links tbody tr").forEach(function (row) {
        row.hidden = row.textContent.toLowerCase().indexOf(v) < 0;
      });
    });
    tables.insertBefore(input, tables.querySelector("summary").nextSibling);
  }
  // Enter or Space selects, as on a button.
  function activate(e) {
    if (e.key !== "Enter" && e.key !== " ") return false;
    e.preventDefault();
    return true;
  }
  function focused() {
    return hasProblem && (scope === "problems" || (scope === "auto" && nodes.length > FOCUS_LIMIT));
  }
  // One rule for the list, the graph and the matrix; the matrix is the full picture, so focus never narrows it.
  function visible(l, full) {
    return show[l.Status] && (full || !focused() || PROBLEM[l.Status]);
  }
  function layoutFor(focus) {
    var k = focus ? "focus" : "all";
    if (!layouts.has(k)) {
      var ns = nodes,
        ls = links;
      if (focus) {
        var keep = new Map();
        links.forEach(function (l) {
          if (PROBLEM[l.Status]) {
            keep.set(l.From, true);
            keep.set(l.To, true);
          }
        });
        ns = nodes.filter(function (n) {
          return keep.get(n.Name);
        });
        ls = links.filter(function (l) {
          return PROBLEM[l.Status];
        });
      }
      layouts.set(k, { nodes: ns, L: layout(ns, ls) });
    }
    return layouts.get(k);
  }
  function pickLink(l) {
    selNode = null;
    sel = sel === l ? null : l;
    render();
  }
  function pickNode(n) {
    sel = null;
    selNode = selNode === n ? null : n;
    render();
  }

  function renderChips() {
    var c = {};
    links.forEach(function (l) {
      c[l.Status] = (c[l.Status] || 0) + 1;
    });
    chips.replaceChildren();
    STATUSES.slice()
      .sort(function (a, b) {
        return TRIAGE[a] - TRIAGE[b];
      })
      .forEach(function (s) {
        if (!c[s]) return;
        var b = h(
          "button",
          {
            type: "button",
            class: "g-chip " + s,
            "aria-pressed": String(show[s] && (PROBLEM[s] || !focused() || mode === "matrix")),
            "data-k": "s" + s,
            title: MEANING[s],
          },
          [h("i"), h("b", { text: String(c[s]) }), " " + LABEL[s]],
        );
        b.addEventListener("click", function () {
          if (!PROBLEM[s] && focused() && mode === "graph") {
            show[s] = true; // asking for the other links means the full picture
            scope = "all";
          } else show[s] = !show[s];
          render();
        });
        chips.appendChild(b);
      });
  }
  function renderList() {
    var problems = links
      .filter(function (l) {
        return PROBLEM[l.Status] && show[l.Status];
      })
      .sort(function (a, b) {
        return TRIAGE[a.Status] - TRIAGE[b.Status] || b.Count - a.Count;
      });
    list.replaceChildren();
    if (!problems.length) {
      list.appendChild(
        h("li", { class: "hint", text: "No violations, advisories or accepted exceptions shown." }),
      );
      return;
    }
    problems.forEach(function (l) {
      var li = h("li", { tabindex: "0", "data-k": "l" + key(l.From, l.To), class: "g-item " + l.Status + (sel === l ? " sel" : "") }, [
        h("i"),
        pairEl(l),
        h("span", { class: "hint", text: count(l) }),
      ]);
      li.addEventListener("click", function () {
        pickLink(l);
      });
      li.addEventListener("keydown", function (e) {
        if (activate(e)) pickLink(l);
      });
      list.appendChild(li);
    });
  }
  function renderGraph() {
    var lay = layoutFor(focused()),
      L = lay.L,
      xy = L.xy;
    var drawn = links.filter(function (l) {
      return xy.has(l.From) && xy.has(l.To) && visible(l);
    });
    var pairs = new Set(
      drawn.map(function (l) {
        return key(l.From, l.To);
      }),
    );
    var svg = svgEl("svg", {
      viewBox: "0 0 " + L.width + " " + L.height,
      width: L.width,
      height: L.height,
      role: "img",
      "aria-label": "Module relationships by policy status",
    });
    var defs = svgEl("defs", {});
    STATUSES.forEach(function (s) {
      var m = svgEl("marker", {
        id: "gm-" + s,
        viewBox: "0 0 10 10",
        refX: 9,
        refY: 5,
        markerWidth: 9,
        markerHeight: 9,
        markerUnits: "userSpaceOnUse",
        orient: "auto-start-reverse",
      });
      m.appendChild(svgEl("path", { d: "M0 0L10 5L0 10z", class: "gm " + s }));
      defs.appendChild(m);
    });
    svg.appendChild(defs);
    L.labels.forEach(function (b) {
      svg.appendChild(svgEl("text", { x: PAD, y: b.y + 12, class: "g-band" }, b.text));
    });
    var related = function (l) {
      return (
        (!sel && selNode === null) ||
        l === sel ||
        (selNode !== null && (l.From === selNode || l.To === selNode))
      );
    };
    drawn
      .slice()
      .sort(function (a, b) {
        return TRIAGE[b.Status] - TRIAGE[a.Status] || a.Count - b.Count;
      })
      .forEach(function (l) {
        var a = xy.get(l.From),
          b = xy.get(l.To),
          d,
          mx,
          my;
        if (l.From === l.To) {
          // a self-link: a loop on the right side of the module
          var rx = a.x + NODE_W,
            ry = a.y + NODE_H / 2;
          d =
            "M" +
            rx +
            " " +
            (ry - 8) +
            " C" +
            (rx + 34) +
            " " +
            (ry - 30) +
            " " +
            (rx + 34) +
            " " +
            (ry + 30) +
            " " +
            (rx + 2) +
            " " +
            (ry + 8);
          mx = rx + 26;
          my = ry;
        } else {
          var down = b.y > a.y,
            x1 = a.x + NODE_W / 2,
            x2 = b.x + NODE_W / 2;
          if (pairs.has(key(l.To, l.From))) {
            var o = down ? -14 : 14;
            x1 += o;
            x2 += o;
          } // a cycle pair draws as two curves
          if (down) {
            var y1 = a.y + NODE_H,
              y2 = b.y - 2;
            d =
              "M" +
              x1 +
              " " +
              y1 +
              " C" +
              x1 +
              " " +
              (y1 + 40) +
              " " +
              x2 +
              " " +
              (y2 - 40) +
              " " +
              x2 +
              " " +
              y2;
            mx = (x1 + x2) / 2;
            my = (y1 + y2) / 2;
          } else {
            var side = x2 >= x1 ? 1 : -1,
              ya = a.y,
              yb = b.y + NODE_H + 2,
              bow = 46;
            if (a.y === b.y) {
              // Same line: an arc over both modules, so the count never sits inside a module box.
              // A pair in both directions: the second one arcs below.
              var below = pairs.has(key(l.To, l.From)) && l.From > l.To,
                top = below ? a.y + NODE_H + 2 : a.y - 2,
                lift = (below ? -1 : 1) * Math.min(ARC, 24 + Math.abs(x2 - x1) / 16);
              d = "M" + x1 + " " + top + " C" + x1 + " " + (top - lift) + " " + x2 + " " + (top - lift) + " " + x2 + " " + top;
              // The count sits on the arc near its source (t = 0.3), so nested arcs on one line keep apart badges.
              mx = 0.784 * x1 + 0.216 * x2;
              my = top - lift * 0.63;
            } else {
            d =
              "M" +
              x1 +
              " " +
              ya +
              " C" +
              (x1 + side * bow) +
              " " +
              (ya - bow) +
              " " +
              (x2 + side * bow) +
              " " +
              (yb + bow) +
              " " +
              x2 +
              " " +
              yb;
            mx = (x1 + x2) / 2 + side * bow * 0.75;
            my = (ya + yb) / 2;
            }
          }
        }
        var g = svgEl("g", {
          class: "g-edge " + l.Status + (related(l) ? "" : " dim") + (l === sel ? " sel" : ""),
          tabindex: PROBLEM[l.Status] ? 0 : -1,
          "data-k": "e" + key(l.From, l.To),
        });
        g.appendChild(svgEl("path", { d: d, class: "hit" }));
        g.appendChild(
          svgEl("path", {
            d: d,
            class: "line",
            "stroke-width": weight(l.Count),
            "marker-end": "url(#gm-" + l.Status + ")",
          }),
        );
        if (PROBLEM[l.Status] || l === sel) {
          var t = count(l),
            w = 8 + t.length * 6;
          g.appendChild(
            svgEl("rect", {
              x: mx - w / 2,
              y: my - 8,
              width: w,
              height: 16,
              rx: 8,
              class: "badge",
            }),
          );
          g.appendChild(
            svgEl("text", { x: mx, y: my + 4, "text-anchor": "middle", class: "badge-t" }, t),
          );
        }
        g.appendChild(
          svgEl(
            "title",
            {},
            l.From + " → " + l.To + " · " + l.Count + " observed dependencies · " + LABEL[l.Status],
          ),
        );
        g.addEventListener("click", function (e) {
          e.stopPropagation();
          pickLink(l);
        });
        g.addEventListener("keydown", function (e) {
          if (activate(e)) pickLink(l);
        });
        svg.appendChild(g);
      });
    var worst = new Map();
    links.forEach(function (l) {
      if (!PROBLEM[l.Status] || !show[l.Status]) return;
      [l.From, l.To].forEach(function (n) {
        if (!worst.has(n) || TRIAGE[l.Status] < TRIAGE[worst.get(n)]) worst.set(n, l.Status);
      });
    });
    lay.nodes.forEach(function (n) {
      var p = xy.get(n.Name);
      var on =
        (!sel && selNode === null) ||
        selNode === n.Name ||
        (sel && (sel.From === n.Name || sel.To === n.Name)) ||
        (selNode !== null &&
          drawn.some(function (l) {
            return (
              (l.From === selNode && l.To === n.Name) || (l.To === selNode && l.From === n.Name)
            );
          }));
      var g = svgEl("g", {
        class:
          "g-node" +
          (worst.has(n.Name) ? " " + worst.get(n.Name) : "") +
          (n.PolicyLine ? "" : " undeclared") +
          (on ? "" : " dim") +
          (selNode === n.Name ? " sel" : ""),
        tabindex: 0,
        "data-k": "n" + JSON.stringify(n.Name),
      });
      g.appendChild(svgEl("rect", { x: p.x, y: p.y, width: NODE_W, height: NODE_H, rx: 6 }));
      g.appendChild(
        svgEl(
          "text",
          { x: p.x + NODE_W / 2, y: p.y + 22, "text-anchor": "middle" },
          n.Name.length > 17 ? n.Name.slice(0, 16) + "…" : n.Name,
        ),
      );
      g.appendChild(svgEl("title", {}, n.Name + (n.PolicyLine ? "" : " · not in the policy")));
      g.addEventListener("click", function (e) {
        e.stopPropagation();
        pickNode(n.Name);
      });
      g.addEventListener("keydown", function (e) {
        if (activate(e)) pickNode(n.Name);
      });
      svg.appendChild(g);
    });
    svg.addEventListener("click", function () {
      if (sel || selNode !== null) {
        sel = null;
        selNode = null;
        render();
      }
    });
    canvas.replaceChildren(svg);
  }
  function renderMatrix() {
    var L = layoutFor(false).L;
    var order = nodes
      .map(function (n) {
        return n.Name;
      })
      .sort(function (a, b) {
        var p = L.xy.get(a),
          r = L.xy.get(b);
        return p.line - r.line || p.x - r.x;
      });
    var cell = new Map();
    links.forEach(function (l) {
      cell.set(key(l.From, l.To), l);
    });
    var head = h("tr", {}, [h("th")]);
    order.forEach(function (c) {
      head.appendChild(h("th", { scope: "col" }, [h("span", { text: c })]));
    });
    var body = h("tbody");
    order.forEach(function (r) {
      var tr = h("tr", {}, [h("th", { scope: "row", text: r })]);
      order.forEach(function (c) {
        var l = cell.get(key(r, c)),
          td = h("td", { class: r === c ? "self" : "" });
        if (l && visible(l, true)) {
          td.className = l.Status + (sel === l ? " sel" : "");
          td.textContent = l.Count ? String(l.Count) : "·";
          td.title = r + " → " + c + " · " + l.Count + " · " + LABEL[l.Status];
          td.tabIndex = 0;
          td.setAttribute("data-k", "c" + key(l.From, l.To));
          td.addEventListener("click", function () {
            pickLink(l);
          });
          td.addEventListener("keydown", function (e) {
            if (activate(e)) pickLink(l);
          });
        }
        tr.appendChild(td);
      });
      body.appendChild(tr);
    });
    matrixBox.replaceChildren(
      h("p", {
        class: "hint",
        text: "Rows depend on columns. Position does not establish permission.",
      }),
      h("div", { class: "g-scroll" }, [
        h("table", { class: "g-dsm" }, [h("thead", {}, [head]), body]),
      ]),
    );
  }
  function renderNote() {
    note.replaceChildren();
    if (mode !== "graph") return;
    var btn = function (label, s) {
      var b = h("button", { type: "button", class: "g-link", text: label });
      b.addEventListener("click", function () {
        scope = s;
        sel = null;
        selNode = null;
        render();
      });
      return b;
    };
    if (focused()) {
      note.append(
        "Showing the " +
          layoutFor(true).nodes.length +
          " of " +
          nodes.length +
          " modules with a violation, advisory or accepted exception. ",
        btn("Show all " + nodes.length, "all"),
        " or use the matrix.",
      );
    } else if (nodes.length > FOCUS_LIMIT && hasProblem) {
      note.append(nodes.length + " modules. ", btn("Problems only", "problems"));
    }
    note.append(
      layoutFor(focused()).L.layered
        ? " Grouped by declared layer."
        : " No layers declared: depth places importers above; position does not establish permission.",
    );
  }
  function policyLink(line) {
    return h("a", { href: policyUrl + "#L" + line, text: "Open in the policy" });
  }
  function renderDetail() {
    detail.replaceChildren();
    if (sel) {
      detail.append(
        h("p", {}, [
          h("strong", {}, [pairEl(sel)]),
          " ",
          h("span", { class: "g-tag " + sel.Status, text: LABEL[sel.Status] }),
        ]),
        h("p", {
          class: "hint",
          text: sel.Count
            ? sel.Count + " observed dependencies"
            : "Named by a finding; the report counts no dependency for this pair.",
        }),
        h("p", { text: MEANING[sel.Status] }),
      );
      // The findings behind the link, as the engine wrote them: engine text, so textContent only.
      (explain.get(key(sel.From, sel.To)) || []).forEach(function (f) {
        detail.append(
          h("p", { class: "g-finding" }, [
            h("code", { text: f.RuleID || "(no rule)" }),
            h("span", { class: "hint", text: " · " + f.Kind + ", " + f.Status }),
            f.Why ? h("span", { class: "g-why", text: f.Why }) : null,
          ]),
        );
      });
    } else if (selNode !== null) {
      var n = nodes.find(function (x) {
        return x.Name === selNode;
      });
      var outs = links.filter(function (x) {
        return x.From === selNode && x.To !== selNode;
      }).length;
      var ins = links.filter(function (x) {
        return x.To === selNode && x.From !== selNode;
      }).length;
      var p = links.filter(function (x) {
        return (x.From === selNode || x.To === selNode) && PROBLEM[x.Status];
      }).length;
      detail.append(
        h("p", {}, [h("strong", {}, [h("code", { text: selNode })])]),
        h("p", {
          text:
            "Depends on " +
            outs +
            ", used by " +
            ins +
            ". " +
            (p ? p + " with a problem." : "No problems."),
        }),
        n && n.PolicyLine
          ? h("p", {}, [policyLink(n.PolicyLine)])
          : h("p", { class: "hint", text: "Not in the policy." }),
      );
    } else {
      detail.append(
        h("p", { class: "hint", text: "Select a problem, an arrow, a cell or a module." }),
      );
    }
  }
  // Rendering replaces the elements, so keyboard focus moves to the element with the same data-k.
  function render() {
    var a = document.activeElement,
      k = a && a.getAttribute && a.getAttribute("data-k");
    draw();
    if (!k) return;
    root.querySelectorAll("[data-k]").forEach(function (e) {
      if (e.getAttribute("data-k") === k) e.focus();
    });
  }
  function draw() {
    renderChips();
    renderList();
    canvas.hidden = mode !== "graph";
    matrixBox.hidden = mode !== "matrix";
    if (mode === "graph") renderGraph();
    else renderMatrix();
    root.querySelectorAll("[data-gview]").forEach(function (b) {
      b.setAttribute("aria-pressed", String(b.dataset.gview === mode));
    });
    renderNote();
    renderDetail();
  }
  root.querySelectorAll("[data-gview]").forEach(function (b) {
    b.addEventListener("click", function () {
      mode = b.dataset.gview;
      render();
    });
  });
  render();
})();
