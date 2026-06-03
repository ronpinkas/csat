// Dashboard: fetch analytics + comments JSON and render KPIs and Chart.js charts.
(function () {
  "use strict";

  var toolbar = document.querySelector(".toolbar");
  var tz = toolbar ? toolbar.getAttribute("data-tz") : "UTC";
  var charts = {};

  function rangeQuery() {
    var from = document.getElementById("from").value;
    var to = document.getElementById("to").value;
    return "from=" + encodeURIComponent(from) + "&to=" + encodeURIComponent(to) + "&tz=" + encodeURIComponent(tz);
  }

  function kpiCard(value, label) {
    return '<div class="kpi"><div class="v">' + value + '</div><div class="l">' + label + "</div></div>";
  }

  function renderKPIs(k) {
    var el = document.getElementById("kpis");
    el.innerHTML =
      kpiCard(k.responses, "Responses") +
      kpiCard(k.csat_avg.toFixed(2) + " / 5", "CSAT average") +
      kpiCard(k.csat_pct.toFixed(0) + "%", "CSAT (top-2-box)") +
      kpiCard(k.ces_avg.toFixed(2) + " / 7", "Effort (CES) avg") +
      kpiCard(k.resolution_rate.toFixed(0) + "%", "Resolution rate");
  }

  function draw(id, config) {
    if (charts[id]) charts[id].destroy();
    charts[id] = new Chart(document.getElementById(id).getContext("2d"), config);
  }

  function renderCharts(d) {
    draw("trendChart", {
      type: "bar",
      data: {
        labels: d.trend.labels,
        datasets: [
          { type: "bar", label: "Responses", data: d.trend.responses, yAxisID: "y", backgroundColor: "#bdd0fb" },
          { type: "line", label: "CSAT avg", data: d.trend.csat_avg, yAxisID: "y1", borderColor: "#2563eb", tension: .25 }
        ]
      },
      options: {
        responsive: true, maintainAspectRatio: false,
        scales: {
          y: { beginAtZero: true, position: "left", title: { display: true, text: "Responses" } },
          y1: { beginAtZero: true, suggestedMax: 5, position: "right", grid: { drawOnChartArea: false }, title: { display: true, text: "CSAT" } }
        }
      }
    });

    draw("csatChart", barConfig(d.csat_distribution.labels, d.csat_distribution.data, "#2563eb"));
    draw("cesChart", barConfig(d.ces_distribution.labels, d.ces_distribution.data, "#15a34a"));

    draw("resChart", {
      type: "doughnut",
      data: { labels: d.resolution.labels, datasets: [{ data: d.resolution.data, backgroundColor: ["#15a34a", "#f5b301", "#d23f3f"] }] },
      options: { responsive: true, maintainAspectRatio: false }
    });
  }

  function barConfig(labels, data, color) {
    return {
      type: "bar",
      data: { labels: labels, datasets: [{ data: data, backgroundColor: color }] },
      options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { display: false } }, scales: { y: { beginAtZero: true, ticks: { precision: 0 } } } }
    };
  }

  function renderComments(list) {
    var el = document.getElementById("comments");
    if (!list.length) { el.innerHTML = '<p class="lede">No comments in this range.</p>'; return; }
    el.innerHTML = list.map(function (c) {
      var when = new Date(c.submitted_at * 1000).toLocaleString();
      var meta = "CSAT " + c.csat + " · CES " + c.ces + " · " + c.resolution + " · " + when;
      return '<div class="c"><div class="meta"></div><div class="body"></div></div>';
    }).join("");
    // Fill text via textContent to avoid any HTML injection from comments.
    var nodes = el.querySelectorAll(".c");
    list.forEach(function (c, i) {
      var when = new Date(c.submitted_at * 1000).toLocaleString();
      nodes[i].querySelector(".meta").textContent = "CSAT " + c.csat + " · CES " + c.ces + " · " + c.resolution + " · " + when;
      nodes[i].querySelector(".body").textContent = c.comment;
    });
  }

  function load() {
    var q = rangeQuery();
    var exportLink = document.getElementById("exportLink");
    if (exportLink) exportLink.href = "/export.csv?" + q;
    document.getElementById("rangeNote").textContent = "Times shown in " + tz;

    fetch("/api/analytics?" + q, { headers: { Accept: "application/json" } })
      .then(function (r) { return r.json(); })
      .then(function (d) { renderKPIs(d.kpis); renderCharts(d); })
      .catch(function () {});

    fetch("/api/comments?" + q, { headers: { Accept: "application/json" } })
      .then(function (r) { return r.json(); })
      .then(function (d) { renderComments(d.comments || []); })
      .catch(function () {});
  }

  var apply = document.getElementById("apply");
  if (apply) apply.addEventListener("click", load);
  load();
})();
