// Progressive enhancement of the policy editor. The form and the problem list work without this file.
(function () {
  "use strict";
  var area = document.getElementById("policy");
  if (!area) return;
  var loaded = area.value;
  var graphLink = document.getElementById("graph-link");
  var problems = document.querySelectorAll("#diagnostics li").length;
  // After Check or a refused save the page holds the submitted text, not the text on the branch.
  var unsaved = area.hasAttribute("data-unsaved");

  // The graph is stale as soon as the buffer differs from the branch text or the server listed problems.
  function mark() {
    if (graphLink) graphLink.classList.toggle("stale", unsaved || area.value !== loaded || problems > 0);
  }

  function lineOf(hash) {
    var m = /^#L(\d+)$/.exec(hash);
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

  area.addEventListener("input", mark);

  // A second click on the same #L link changes no hash, so the list answers clicks itself.
  var list = document.getElementById("diagnostics");
  if (list) {
    list.addEventListener("click", function (event) {
      var link = event.target.closest("a[href^='#L']");
      if (!link) return;
      event.preventDefault();
      revealInTextarea(lineOf(link.getAttribute("href")));
    });
  }

  mark();
  revealInTextarea(lineOf(location.hash));
})();
