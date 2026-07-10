// Admin link generator. Reads the per-survey config embedded on the page, mints
// links via the server (POST /api/links), and lets the admin copy one or
// download many. No secret is handled client-side.
(function () {
  "use strict";
  var app = document.getElementById("links-app");
  if (!app) return;

  var config = { sets: [], base: "" };
  try { config = JSON.parse(app.getAttribute("data-config")); } catch (e) { /* leave default */ }
  var csrf = app.getAttribute("data-csrf") || "";

  var $ = function (id) { return document.getElementById(id); };
  function setById(id) {
    for (var i = 0; i < config.sets.length; i++) {
      if (String(config.sets[i].id) === String(id)) return config.sets[i];
    }
    return null;
  }
  function currentSet() { return setById($("link-set").value); }

  // ---- POST to the mint API (form-encoded so the CSRF middleware sees it) ----
  function generate(entries, cb) {
    var s = currentSet();
    var payload = JSON.stringify({ set: s ? s.id : 0, lang: "en", entries: entries });
    var body = "csrf=" + encodeURIComponent(csrf) + "&payload=" + encodeURIComponent(payload);
    fetch("/api/links", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body,
    }).then(function (r) {
      if (!r.ok) throw new Error("Server returned " + r.status);
      return r.json();
    }).then(function (data) { cb(null, data.results || []); })
      .catch(function (err) { cb(err); });
  }

  // ---- prefill inputs (auto-detected per survey) ----
  function renderPrefills() {
    var s = currentSet();
    var wrap = $("single-prefills");
    wrap.textContent = "";
    if (!s) return;
    var prefills = s.prefills || [];
    prefills.forEach(function (f) {
      var row = document.createElement("div"); row.className = "form-row";
      var lab = document.createElement("label"); lab.textContent = f.label;
      var inp = document.createElement("input"); inp.className = "input";
      inp.setAttribute("data-param", f.param); inp.autocomplete = "off";
      inp.placeholder = f.label;
      row.appendChild(lab); row.appendChild(inp); wrap.appendChild(row);
    });
    var cols = ["subject"].concat(prefills.map(function (f) { return f.param; }));
    $("bulk-cols").textContent = cols.join(", ");
  }

  function collectSingleParams() {
    var params = {};
    $("single-prefills").querySelectorAll("input[data-param]").forEach(function (inp) {
      var v = inp.value.trim();
      if (v) params[inp.getAttribute("data-param")] = v;
    });
    return params;
  }

  // ---- tabs ----
  function showTab(which) {
    $("tab-single").hidden = which !== "single";
    $("tab-bulk").hidden = which !== "bulk";
    $("tab-single-btn").classList.toggle("active", which === "single");
    $("tab-bulk-btn").classList.toggle("active", which === "bulk");
  }

  // ---- CSV parsing (quoted fields supported) ----
  function parseCSV(text) {
    var rows = [], row = [], field = "", inQ = false;
    for (var i = 0; i < text.length; i++) {
      var c = text[i];
      if (inQ) {
        if (c === '"') { if (text[i + 1] === '"') { field += '"'; i++; } else inQ = false; }
        else field += c;
      } else if (c === '"') inQ = true;
      else if (c === ",") { row.push(field); field = ""; }
      else if (c === "\n" || c === "\r") {
        if (c === "\r" && text[i + 1] === "\n") i++;
        row.push(field); field = "";
        if (row.length > 1 || row[0] !== "") rows.push(row);
        row = [];
      } else field += c;
    }
    if (field !== "" || row.length) { row.push(field); rows.push(row); }
    return rows;
  }

  function csvToEntries(text) {
    var rows = parseCSV(text);
    if (!rows.length) return { error: "No rows found." };
    var header = rows[0].map(function (h) { return h.trim().toLowerCase(); });
    var iSubject = header.indexOf("subject");
    if (iSubject === -1) iSubject = header.indexOf("email");
    if (iSubject === -1) return { error: "CSV needs a 'subject' (or 'email') column." };
    var s = currentSet();
    var paramCols = {};
    ((s && s.prefills) || []).forEach(function (f) {
      var idx = header.indexOf(f.param.toLowerCase());
      if (idx !== -1) paramCols[f.param] = idx;
    });
    var entries = [];
    for (var r = 1; r < rows.length; r++) {
      var subj = (rows[r][iSubject] || "").trim();
      if (!subj) continue;
      var params = {};
      Object.keys(paramCols).forEach(function (p) {
        var v = (rows[r][paramCols[p]] || "").trim();
        if (v) params[p] = v;
      });
      entries.push({ subject: subj, params: params });
    }
    return { entries: entries };
  }

  // ---- results ----
  var lastResults = [];
  function renderBulk(results) {
    lastResults = results;
    var tb = $("bulk-tbody"); tb.textContent = "";
    var ok = 0;
    results.forEach(function (res) {
      var tr = document.createElement("tr");
      var td1 = document.createElement("td"); td1.textContent = res.subject || "";
      var td2 = document.createElement("td");
      if (res.url) { td2.textContent = res.url; ok++; }
      else { td2.textContent = "⚠ " + (res.error || "error"); td2.className = "muted"; }
      tr.appendChild(td1); tr.appendChild(td2); tb.appendChild(tr);
    });
    $("bulk-count").textContent = ok + " link(s) generated" +
      (results.length - ok ? " · " + (results.length - ok) + " skipped" : "");
    $("bulk-result").hidden = false;
    showErr("bulk-success", "✓ " + ok + " link" + (ok === 1 ? "" : "s") +
      " generated — download the CSV or copy from the table below.");
  }

  function downloadCSV() {
    var esc = function (c) { c = String(c == null ? "" : c); return /[",\n]/.test(c) ? '"' + c.replace(/"/g, '""') + '"' : c; };
    var lines = [["subject", "url", "error"].join(",")];
    lastResults.forEach(function (r) { lines.push([esc(r.subject), esc(r.url || ""), esc(r.error || "")].join(",")); });
    var blob = new Blob([lines.join("\n") + "\n"], { type: "text/csv" });
    var a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "survey_links.csv";
    document.body.appendChild(a); a.click(); document.body.removeChild(a);
    URL.revokeObjectURL(a.href);
  }

  function showErr(id, msg) { var e = $(id); e.textContent = msg; e.hidden = !msg; }

  // Buttons stay disabled until their required field has content.
  function updateSingleBtn() { $("single-generate").disabled = !$("single-subject").value.trim(); }
  function updateBulkBtn() { $("bulk-generate").disabled = !$("bulk-input").value.trim(); }

  // ---- wire ----
  $("link-set").addEventListener("change", renderPrefills);
  $("single-subject").addEventListener("input", function () {
    updateSingleBtn();
    $("single-result").hidden = true; // a shown link is stale once the subject changes
    showErr("single-success", "");
  });
  $("bulk-input").addEventListener("input", function () {
    updateBulkBtn();
    $("bulk-result").hidden = true;
    showErr("bulk-success", "");
  });
  $("tab-single-btn").addEventListener("click", function () { showTab("single"); });
  $("tab-bulk-btn").addEventListener("click", function () { showTab("bulk"); });

  $("single-generate").addEventListener("click", function () {
    showErr("single-error", "");
    var subj = $("single-subject").value.trim();
    if (!subj) { showErr("single-error", "Enter a subject (e.g. the person's email)."); return; }
    generate([{ subject: subj, params: collectSingleParams() }], function (err, results) {
      if (err) { showErr("single-error", "Could not generate: " + err.message); return; }
      var res = results[0] || {};
      if (res.error) { showErr("single-error", res.error); return; }
      $("single-url").value = res.url || "";
      $("single-result").hidden = false;
      showErr("single-success", "✓ Link ready — copy it below and send it to " + subj + ".");
    });
  });

  $("single-copy").addEventListener("click", function () {
    var url = $("single-url");
    url.select();
    if (navigator.clipboard) navigator.clipboard.writeText(url.value);
    else document.execCommand("copy");
    var btn = $("single-copy");
    btn.textContent = "✓ Copied"; btn.classList.add("copied");
    setTimeout(function () { btn.textContent = "Copy"; btn.classList.remove("copied"); }, 1500);
  });

  $("bulk-file").addEventListener("change", function (e) {
    var f = e.target.files && e.target.files[0];
    if (!f) return;
    var reader = new FileReader();
    reader.onload = function () { $("bulk-input").value = reader.result; updateBulkBtn(); };
    reader.readAsText(f);
  });

  $("bulk-generate").addEventListener("click", function () {
    showErr("bulk-error", "");
    var text = $("bulk-input").value.trim();
    if (!text) { showErr("bulk-error", "Paste some rows or upload a CSV first."); return; }
    var parsed = csvToEntries(text);
    if (parsed.error) { showErr("bulk-error", parsed.error); return; }
    if (!parsed.entries.length) { showErr("bulk-error", "No data rows found."); return; }
    generate(parsed.entries, function (err, results) {
      if (err) { showErr("bulk-error", "Could not generate: " + err.message); return; }
      renderBulk(results);
    });
  });

  $("bulk-download").addEventListener("click", downloadCSV);

  // ---- init ----
  renderPrefills();
  showTab("single");
  updateSingleBtn();
  updateBulkBtn();
})();
