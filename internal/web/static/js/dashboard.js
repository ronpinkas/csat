// Dashboard: fetch analytics + comments JSON and build KPI tiles and a chart
// card per question, adapting to whatever the survey definition contains.
(function () {
  "use strict";

  var toolbar = document.querySelector(".toolbar");
  var tz = toolbar ? toolbar.getAttribute("data-tz") : "UTC";
  var palette = ["#15a34a", "#f5b301", "#d23f3f", "#2563eb", "#7c3aed", "#0891b2", "#db2777", "#65a30d"];
  var charts = {};

  function rangeQuery() {
    var from = document.getElementById("from").value;
    var to = document.getElementById("to").value;
    var q = "from=" + encodeURIComponent(from) + "&to=" + encodeURIComponent(to) + "&tz=" + encodeURIComponent(tz);
    var setSel = document.getElementById("setSelect");
    if (setSel && setSel.value) q += "&set=" + encodeURIComponent(setSel.value);
    // Drafts (saved but never submitted) are excluded unless explicitly included.
    var inc = document.getElementById("includeIncomplete");
    if (inc && inc.checked) q += "&incomplete=1";
    return q;
  }
  function el(tag, cls) { var e = document.createElement(tag); if (cls) e.className = cls; return e; }
  function round2(x) { return Math.round(x * 100) / 100; }
  function destroyCharts() { Object.keys(charts).forEach(function (k) { charts[k].destroy(); }); charts = {}; }

  function kpi(value, label) {
    var c = el("div", "kpi");
    var v = el("div", "v"); v.textContent = value; c.appendChild(v);
    var l = el("div", "l"); l.textContent = label; c.appendChild(l);
    return c;
  }

  function barChart(canvas, labels, data) {
    return new Chart(canvas, {
      type: "bar",
      data: { labels: labels, datasets: [{ data: data, backgroundColor: "#2563eb" }] },
      options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { display: false } },
        scales: { y: { beginAtZero: true, ticks: { precision: 0 } } } }
    });
  }

  function render(d) {
    destroyCharts();

    // KPIs: responses + one tile per numeric question
    var kpis = document.getElementById("kpis");
    kpis.innerHTML = "";
    kpis.appendChild(kpi(d.responses, "Responses"));
    d.questions.forEach(function (q) {
      if (q.avg == null) return;
      if (q.type === "nps") kpis.appendChild(kpi(round2(q.nps), q.label + " (NPS)"));
      else kpis.appendChild(kpi(round2(q.avg) + " / " + q.max, q.label));
    });

    // overall responses trend
    charts.trend = new Chart(document.getElementById("trendChart"), {
      type: "bar",
      data: { labels: d.trend.labels, datasets: [{ label: "Responses", data: d.trend.responses, backgroundColor: "#bdd0fb" }] },
      options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { display: false } },
        scales: { y: { beginAtZero: true, ticks: { precision: 0 } } } }
    });

    // one card per question (text questions surface in Recent comments)
    var area = document.getElementById("charts");
    area.innerHTML = "";
    d.questions.forEach(function (q, i) {
      if (q.type === "text") return;
      var card = el("div", "chart-card");
      var h = el("h3");
      var extra = "";
      if (q.top_box_pct != null) extra = " · " + Math.round(q.top_box_pct) + "% top-box";
      else if (q.nps != null) extra = " · NPS " + round2(q.nps);
      h.textContent = q.label + extra;
      card.appendChild(h);
      var box = el("div", "cbox");
      var cv = el("canvas"); cv.id = "q_" + i; box.appendChild(cv); card.appendChild(box);
      area.appendChild(card);

      if (q.distribution) {
        charts["q" + i] = barChart(cv, q.distribution.labels, q.distribution.data);
      } else if (q.breakdown) {
        charts["q" + i] = new Chart(cv, {
          type: "doughnut",
          data: { labels: q.breakdown.labels, datasets: [{ data: q.breakdown.data, backgroundColor: palette }] },
          options: { responsive: true, maintainAspectRatio: false }
        });
      }
    });

    document.getElementById("rangeNote").textContent = "Times shown in " + tz;
    var ex = document.getElementById("exportLink");
    if (ex) ex.href = "/export.csv?" + rangeQuery();
  }

  function renderComments(list) {
    var box = document.getElementById("comments");
    if (!list.length) { box.innerHTML = '<p class="lede">No comments in this range.</p>'; return; }
    box.innerHTML = "";
    list.forEach(function (c) {
      var item = el("div", "c");
      var meta = el("div", "meta");
      meta.textContent = (c.question ? c.question + " · " : "") + new Date(c.submitted_at * 1000).toLocaleString();
      var body = el("div", "body");
      body.textContent = c.text;
      item.appendChild(meta); item.appendChild(body);
      box.appendChild(item);
    });
  }

  // Lowest-rating feedback: response-centric, so a 1-star response that left no
  // written comment still appears (that is the point of the section).
  var lowShown = 0; // rows already on screen, so "Show more" knows what to ask for

  function renderLowRatings(d, append) {
    var card = document.getElementById("lowrating-card");
    var box = document.getElementById("lowratings");
    if (!card || !box) return;
    var rows = d.rows || [];
    if (!d.question) { card.hidden = true; return; }
    card.hidden = false;

    document.getElementById("lowRatingTitle").textContent =
      "Rated " + d.rating + " — " + d.question;
    var count = document.getElementById("lowRatingCount");
    count.textContent = d.total + (d.total === 1 ? " response" : " responses");

    if (!append) { box.innerHTML = ""; lowShown = 0; }
    else { var old = box.querySelector(".more-wrap"); if (old) box.removeChild(old); }
    if (!rows.length && !lowShown) {
      box.innerHTML = '<p class="lede">No responses rated ' + d.rating + " in this range.</p>";
      return;
    }
    lowShown += rows.length;
    rows.forEach(function (row) {
      var item = el("div", "c");
      var meta = el("div", "meta");
      meta.textContent = new Date(row.submitted_at * 1000).toLocaleString() +
        (row.subject ? " · " + row.subject : "");
      item.appendChild(meta);
      if (row.comments && row.comments.length) {
        row.comments.forEach(function (c) {
          var body = el("div", "body");
          if (row.comments.length > 1 && c.question) {
            var q = el("span", "cq");
            q.textContent = c.question + ": ";
            body.appendChild(q);
          }
          body.appendChild(document.createTextNode(c.text));
          item.appendChild(body);
        });
      } else {
        var none = el("div", "body muted");
        none.textContent = "(no comment)";
        item.appendChild(none);
      }
      if (row.answers && row.answers.length) {
        var chips = el("div", "chips");
        row.answers.forEach(function (a) {
          var chip = el("span", "chip");
          var k = el("span", "k"); k.textContent = a.question;
          chip.appendChild(k);
          chip.appendChild(document.createTextNode(a.value));
          chips.appendChild(chip);
        });
        item.appendChild(chips);
      }
      box.appendChild(item);
    });
    if (d.total > lowShown && rows.length) {
      var wrap = el("div", "more-wrap");
      var note = el("span", "lede");
      note.textContent = "Showing " + lowShown + " of " + d.total + ". ";
      var more = el("button", "btn ghost small");
      more.type = "button";
      more.textContent = "Show more";
      more.addEventListener("click", function () { loadLowRatings((d.page || 0) + 1, true); });
      wrap.appendChild(note); wrap.appendChild(more);
      box.appendChild(wrap);
    }
  }

  function loadLowRatings(page, append) {
    fetch("/api/lowratings?" + rangeQuery() + "&page=" + page, { headers: { Accept: "application/json" } })
      .then(function (r) { return r.json(); })
      .then(function (d) { renderLowRatings(d, append); })
      .catch(function () {});
  }

  function load() {
    var q = rangeQuery();
    fetch("/api/analytics?" + q, { headers: { Accept: "application/json" } })
      .then(function (r) { return r.json(); }).then(render).catch(function () {});
    fetch("/api/comments?" + q, { headers: { Accept: "application/json" } })
      .then(function (r) { return r.json(); }).then(function (d) { renderComments(d.comments || []); }).catch(function () {});
    loadLowRatings(0, false);
  }

  var apply = document.getElementById("apply");
  if (apply) apply.addEventListener("click", load);
  var setSel = document.getElementById("setSelect");
  if (setSel) setSel.addEventListener("change", load);
  // Toggling drafts re-runs load(), which also refreshes the export link.
  var inc = document.getElementById("includeIncomplete");
  if (inc) inc.addEventListener("change", load);
  load();
})();
