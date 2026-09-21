// Progressive enhancement of the policy editor. The form works without this file.
(function () {
  "use strict";
  var area = document.getElementById("policy");
  if (!area) return;
  var loaded = area.value;
  var graphLink = document.getElementById("graph-link");

  // The graph is stale as soon as the buffer differs from the loaded text or has diagnostics.
  function mark(value, diagnostics) {
    if (graphLink) graphLink.classList.toggle("stale", value !== loaded || diagnostics > 0);
  }

  function lineFromHash() {
    var m = /^#L(\d+)$/.exec(location.hash);
    return m ? parseInt(m[1], 10) : 0;
  }

  function revealInTextarea(line) {
    if (!line) return;
    var lines = area.value.split("\n");
    var offset = 0;
    for (var i = 0; i < line - 1 && i < lines.length; i++) offset += lines[i].length + 1;
    area.focus();
    area.setSelectionRange(offset, offset + (lines[line - 1] || "").length);
    var lineHeight = parseFloat(getComputedStyle(area).lineHeight) || 20;
    area.scrollTop = Math.max(0, (line - 5) * lineHeight);
  }

  area.addEventListener("input", function () { mark(area.value, 0); });

  // The optional vendored bundle (static/vendor/README.md) publishes window.ArchfitEditor.
  var container = document.getElementById("monaco");
  if (window.ArchfitEditor && container) {
    container.hidden = false;
    area.hidden = true;
    window.ArchfitEditor.mount(container, {
      value: area.value,
      schemaUrl: area.dataset.schema,
      line: lineFromHash(),
      onChange: function (value, diagnostics) {
        area.value = value;
        mark(value, diagnostics);
      }
    });
  } else {
    revealInTextarea(lineFromHash());
  }
})();
