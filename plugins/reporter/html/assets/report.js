(function () {
  "use strict";
  var filters = document.querySelector(".filters");
  if (filters) { filters.hidden = false; }
  var items = Array.prototype.slice.call(document.querySelectorAll("tbody.item"));

  function setOpen(item, open) {
    var toggle = item.querySelector(".toggle");
    if (!toggle) { return; }
    item.classList.toggle("collapsed", !open);
    toggle.setAttribute("aria-expanded", open ? "true" : "false");
  }

  // Collapse details by default; without JavaScript they stay visible.
  items.forEach(function (item) {
    var toggle = item.querySelector(".toggle");
    if (!toggle) { return; }
    setOpen(item, false);
    toggle.addEventListener("click", function () {
      setOpen(item, item.classList.contains("collapsed"));
    });
  });
  document.querySelectorAll("[data-expand]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var open = btn.getAttribute("data-expand") === "all";
      items.forEach(function (item) {
        if (!item.classList.contains("hidden")) { setOpen(item, open); }
      });
    });
  });

  function value(id) {
    var el = document.getElementById(id);
    return el ? el.value.trim().toLowerCase() : "";
  }

  // A vulnerability row is one package version with several advisories:
  // data-findings lists the severity of each, so filters and counts see them.
  function severities(item) {
    return (item.dataset.findings || item.dataset.severity || "").split(" ");
  }

  function apply() {
    var project = value("f-project"), eco = value("f-ecosystem"), sev = value("f-severity"),
        direct = value("f-direct"), built = value("f-built"), q = value("f-search");
    items.forEach(function (item) {
      var d = item.dataset;
      var show = (!project || d.project.toLowerCase() === project) &&
        (!eco || d.ecosystem === eco) &&
        (!sev || severities(item).indexOf(sev) >= 0) &&
        (!direct || d.direct === direct) &&
        (!built || (d.built || "built") === built) &&
        (!q || d.search.indexOf(q) >= 0);
      item.classList.toggle("hidden", !show);
    });
    document.querySelectorAll("[data-count-for]").forEach(function (badge) {
      var table = document.getElementById(badge.getAttribute("data-count-for"));
      if (table) { badge.textContent = table.querySelectorAll("tbody.item:not(.hidden)").length; }
    });
    // Severity counters count built findings; not-built ones have their own.
    var counts = {}, notBuilt = 0;
    items.forEach(function (item) {
      if (item.classList.contains("hidden")) { return; }
      var sevs = severities(item);
      if (item.dataset.built === "not-built") { notBuilt += sevs.length; return; }
      sevs.forEach(function (s) { counts[s] = (counts[s] || 0) + 1; });
    });
    var nb = document.querySelector("[data-summary-notbuilt] .num");
    if (nb) { nb.textContent = notBuilt; }
    document.querySelectorAll("[data-summary]").forEach(function (card) {
      var n = counts[card.getAttribute("data-summary")] || 0;
      card.querySelector(".num").textContent = n;
      card.classList.toggle("zero", n === 0);
    });
  }

  ["f-project", "f-ecosystem", "f-severity", "f-direct", "f-built", "f-search"].forEach(function (id) {
    var el = document.getElementById(id);
    if (el) { el.addEventListener(el.tagName === "INPUT" ? "input" : "change", apply); }
  });

  // "/" focuses the search box, Escape clears it.
  document.addEventListener("keydown", function (e) {
    var search = document.getElementById("f-search");
    if (!search) { return; }
    var active = document.activeElement;
    var typing = active && /^(INPUT|SELECT|TEXTAREA)$/.test(active.tagName);
    if (e.key === "/" && !typing) {
      e.preventDefault();
      search.focus();
    } else if (e.key === "Escape" && active === search) {
      search.value = "";
      apply();
      search.blur();
    }
  });

  document.querySelectorAll("table.findings > thead th[data-sort]").forEach(function (th) {
    th.addEventListener("click", function () {
      var table = th.closest("table");
      var col = Array.prototype.indexOf.call(th.parentNode.children, th);
      var desc = th.getAttribute("aria-sort") !== "descending";
      table.querySelectorAll(":scope > thead th").forEach(function (h) { h.removeAttribute("aria-sort"); });
      th.setAttribute("aria-sort", desc ? "descending" : "ascending");
      var rows = Array.prototype.slice.call(table.querySelectorAll(":scope > tbody.item"));
      var key = function (item) {
        if (th.getAttribute("data-sort") === "rank") { return Number(item.dataset.rank || 0); }
        var cell = item.querySelector("tr.main").children[col];
        return cell ? cell.textContent.trim().toLowerCase() : "";
      };
      rows.sort(function (a, b) {
        var x = key(a), y = key(b);
        var c = x < y ? -1 : x > y ? 1 : 0;
        return desc ? -c : c;
      });
      rows.forEach(function (r) { table.appendChild(r); });
    });
  });
})();
