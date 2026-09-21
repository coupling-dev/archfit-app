// Draws the read-only module graph as SVG: layers are columns, links are curves. The tables below the
// drawing are rendered by the server and stay the fallback.
(function () {
  "use strict";
  var NS = "http://www.w3.org/2000/svg";
  var MAX_DRAWN = 300, W = 180, H = 28, GAP_X = 120, GAP_Y = 14, TOP = 40, PAD = 10, BEND = 60;

  var data = document.getElementById("graph-data");
  var host = document.getElementById("graph");
  if (!data || !host) return;
  var view = JSON.parse(data.textContent);
  var nodes = view.Nodes || [], links = view.Links || [];

  function el(name, attrs, text) {
    var e = document.createElementNS(NS, name);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (text !== undefined) e.textContent = text;
    return e;
  }

  // Above the drawing limit the tables become a filterable list.
  function filterable() {
    var input = document.createElement("input");
    input.type = "search";
    input.placeholder = "Filter modules";
    input.addEventListener("input", function () {
      var q = input.value.toLowerCase();
      document.querySelectorAll("#graph-nodes tbody tr, #graph-links tbody tr").forEach(function (row) {
        row.hidden = row.textContent.toLowerCase().indexOf(q) < 0;
      });
    });
    host.appendChild(input);
  }

  if (nodes.length > MAX_DRAWN) { filterable(); return; }
  if (nodes.length === 0) return;

  var columns = [], byLayer = {}, place = {};
  nodes.forEach(function (n) {
    var layer = n.Layer || "";
    if (!(layer in byLayer)) { byLayer[layer] = columns.length; columns.push([]); }
    columns[byLayer[layer]].push(n);
  });
  var rows = Math.max.apply(null, columns.map(function (c) { return c.length; }));
  var svg = el("svg", {
    width: PAD * 2 + columns.length * W + (columns.length - 1) * GAP_X,
    height: TOP + rows * (H + GAP_Y) + PAD,
    role: "img", "aria-label": "Module graph"
  });
  var defs = el("defs", {});
  var marker = el("marker", { id: "arrow", viewBox: "0 0 10 10", refX: 10, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" });
  marker.appendChild(el("path", { d: "M0,0 L10,5 L0,10 z" }));
  defs.appendChild(marker);
  svg.appendChild(defs);

  var linkLayer = el("g", {});
  svg.appendChild(linkLayer);
  var policyUrl = host.dataset.policyUrl;

  columns.forEach(function (col, ci) {
    var x = PAD + ci * (W + GAP_X);
    svg.appendChild(el("text", { x: x, y: 20, "class": "layer" }, col[0].Layer || "(no layer)"));
    col.forEach(function (n, ri) {
      var y = TOP + ri * (H + GAP_Y);
      place[n.Name] = { x: x, y: y + H / 2 };
      var g = el("g", {});
      g.appendChild(el("rect", { x: x, y: y, width: W, height: H, rx: 4 }));
      g.appendChild(el("text", { x: x + 8, y: y + 18 }, n.Name.length > 24 ? n.Name.slice(0, 23) + "…" : n.Name));
      g.appendChild(el("title", {}, n.Name));
      if (n.PolicyLine) {
        var a = el("a", { href: policyUrl + "#L" + n.PolicyLine });
        a.appendChild(g);
        g = a;
      }
      svg.appendChild(g);
    });
  });

  links.forEach(function (l) {
    var a = place[l.From], b = place[l.To];
    if (!a || !b) return;
    // Leave the source on the side that faces the target; links inside one column loop out to the right.
    var right = b.x >= a.x, into = b.x > a.x;
    var x1 = right ? a.x + W : a.x, x2 = into ? b.x : b.x + W;
    var d = "M" + x1 + "," + a.y + " C" + (x1 + (right ? BEND : -BEND)) + "," + a.y + " " +
      (x2 + (into ? -BEND : BEND)) + "," + b.y + " " + x2 + "," + b.y;
    var cls = (l.Permitted ? "" : "violation") + (l.Observed ? "" : " unobserved");
    var p = el("path", { d: d, "class": cls.trim(), "marker-end": "url(#arrow)" });
    p.appendChild(el("title", {}, l.From + " → " + l.To + (l.Permitted ? "" : " (not permitted)")));
    linkLayer.appendChild(p);
  });

  host.appendChild(svg);
})();
