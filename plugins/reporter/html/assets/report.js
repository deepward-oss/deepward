(function () {
  "use strict";
  var filters = document.querySelector(".filters");
  if (filters) { filters.hidden = false; }
  var items = Array.prototype.slice.call(document.querySelectorAll("tbody.item"));

  // Collapse details by default; without JavaScript they stay visible.
  items.forEach(function (item) {
    var toggle = item.querySelector(".toggle");
    if (!toggle) { return; }
    item.classList.add("collapsed");
    toggle.setAttribute("aria-expanded", "false");
    toggle.addEventListener("click", function () {
      var collapsed = item.classList.toggle("collapsed");
      toggle.setAttribute("aria-expanded", collapsed ? "false" : "true");
    });
  });

  function value(id) {
    var el = document.getElementById(id);
    return el ? el.value.trim().toLowerCase() : "";
  }

  function apply() {
    var project = value("f-project"), eco = value("f-ecosystem"), sev = value("f-severity"),
        direct = value("f-direct"), built = value("f-built"), q = value("f-search");
    items.forEach(function (item) {
      var d = item.dataset;
      var show = (!project || d.project.toLowerCase() === project) &&
        (!eco || d.ecosystem === eco) &&
        (!sev || d.severity === sev) &&
        (!direct || d.direct === direct) &&
        (!built || (d.built || "built") === built) &&
        (!q || d.search.indexOf(q) >= 0);
      item.classList.toggle("hidden", !show);
    });
    document.querySelectorAll("[data-count-for]").forEach(function (badge) {
      var table = document.getElementById(badge.getAttribute("data-count-for"));
      if (table) { badge.textContent = table.querySelectorAll("tbody.item:not(.hidden)").length; }
    });
    // Severity cards count built findings; not-built ones have their own card.
    var counts = {}, notBuilt = 0;
    items.forEach(function (item) {
      if (item.classList.contains("hidden")) { return; }
      if (item.dataset.built === "not-built") { notBuilt++; return; }
      counts[item.dataset.severity] = (counts[item.dataset.severity] || 0) + 1;
    });
    var nb = document.querySelector("[data-summary-notbuilt] .num");
    if (nb) { nb.textContent = notBuilt; }
    document.querySelectorAll("[data-summary]").forEach(function (card) {
      card.querySelector(".num").textContent = counts[card.getAttribute("data-summary")] || 0;
    });
  }

  ["f-project", "f-ecosystem", "f-severity", "f-direct", "f-built", "f-search"].forEach(function (id) {
    var el = document.getElementById(id);
    if (el) { el.addEventListener(el.tagName === "INPUT" ? "input" : "change", apply); }
  });

  document.querySelectorAll("table.findings th[data-sort]").forEach(function (th, col) {
    th.addEventListener("click", function () {
      var table = th.closest("table");
      var desc = th.getAttribute("aria-sort") !== "descending";
      table.querySelectorAll("th").forEach(function (h) { h.removeAttribute("aria-sort"); });
      th.setAttribute("aria-sort", desc ? "descending" : "ascending");
      var rows = Array.prototype.slice.call(table.querySelectorAll("tbody.item"));
      var key = function (item) {
        if (th.getAttribute("data-sort") === "rank") { return Number(item.dataset.rank || 0); }
        var cell = item.querySelector("tr.main").children[Array.prototype.indexOf.call(th.parentNode.children, th)];
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
