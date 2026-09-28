// Archfit prototypes — shared behavior: theme toggle, underline tabs, notes drawer.
(function () {
  "use strict";
  var root = document.documentElement;
  var THEME_KEY = "archfit-theme";

  function applyTheme(mode) {
    if (mode === "light" || mode === "dark") root.setAttribute("data-theme", mode);
    else root.removeAttribute("data-theme");
    document.querySelectorAll("[data-theme-set]").forEach(function (b) {
      b.setAttribute("aria-pressed", String(b.getAttribute("data-theme-set") === (mode || "auto")));
    });
  }
  var saved = null;
  try { saved = localStorage.getItem(THEME_KEY); } catch (e) { saved = null; }
  applyTheme(saved || "auto");
  document.addEventListener("click", function (ev) {
    var b = ev.target.closest("[data-theme-set]");
    if (!b) return;
    var mode = b.getAttribute("data-theme-set");
    applyTheme(mode);
    try { localStorage.setItem(THEME_KEY, mode); } catch (e) { /* storage unavailable */ }
  });

  // Tabs: .unav [role=tab][aria-controls]; any [data-goto="panel-id"] also switches.
  function showTab(id, push) {
    var tab = document.querySelector('.unav [role="tab"][aria-controls="' + id + '"]');
    if (!tab) return false;
    document.querySelectorAll('.unav [role="tab"]').forEach(function (t) {
      var on = t === tab;
      t.setAttribute("aria-selected", String(on));
      t.tabIndex = on ? 0 : -1;
      var p = document.getElementById(t.getAttribute("aria-controls"));
      if (p) p.hidden = !on;
    });
    if (push) { try { history.replaceState(null, "", "#" + id); } catch (e) { /* ignore */ } }
    document.dispatchEvent(new CustomEvent("archfit:tab", { detail: { id: id } }));
    return true;
  }
  window.archfitShowTab = function (id) { if (showTab(id, true)) window.scrollTo({ top: 0 }); };
  document.addEventListener("click", function (ev) {
    var t = ev.target.closest('.unav [role="tab"]');
    if (t) { ev.preventDefault(); showTab(t.getAttribute("aria-controls"), true); return; }
    var g = ev.target.closest("[data-goto]");
    if (g) { ev.preventDefault(); window.archfitShowTab(g.getAttribute("data-goto")); }
  });
  document.addEventListener("keydown", function (ev) {
    var t = ev.target.closest && ev.target.closest('.unav [role="tab"]');
    if (!t || (ev.key !== "ArrowRight" && ev.key !== "ArrowLeft")) return;
    var tabs = Array.prototype.slice.call(document.querySelectorAll('.unav [role="tab"]'));
    var i = tabs.indexOf(t) + (ev.key === "ArrowRight" ? 1 : -1);
    var next = tabs[(i + tabs.length) % tabs.length];
    next.focus(); showTab(next.getAttribute("aria-controls"), true);
  });
  var hash = (location.hash || "").slice(1);
  if (!(hash && /^[A-Za-z0-9._~-]+$/.test(hash) && showTab(hash, false))) {
    var first = document.querySelector('.unav [role="tab"][aria-selected="true"]') || document.querySelector('.unav [role="tab"]');
    if (first) showTab(first.getAttribute("aria-controls"), false);
  }

  // Notes drawer: [data-open-notes] opens #notes-drawer; Esc or the backdrop closes it.
  var drawer = document.getElementById("notes-drawer");
  var backdrop = document.getElementById("notes-backdrop");
  function setDrawer(open) {
    if (!drawer || !backdrop) return;
    drawer.hidden = !open; backdrop.hidden = !open;
    if (open) { var f = drawer.querySelector("button, a"); if (f) f.focus(); }
  }
  document.addEventListener("click", function (ev) {
    if (ev.target.closest("[data-open-notes]")) setDrawer(true);
    else if (ev.target.closest("[data-close-notes]") || ev.target === backdrop) setDrawer(false);
  });
  document.addEventListener("keydown", function (ev) { if (ev.key === "Escape") setDrawer(false); });
})();
