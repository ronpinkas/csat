// Visual survey designer: edit a question set as cards and serialize to the
// survey.json shape the server validates + versions. No framework.
(function () {
  "use strict";

  var TYPES = [
    ["stars", "Star rating"],
    ["scale", "Scale (numbered)"],
    ["nps", "NPS (0–10)"],
    ["choice", "Single choice"],
    ["multichoice", "Multiple choice"],
    ["text", "Free text"],
    ["number", "Number"],
    ["date", "Date"],
    ["section", "Section heading"],
  ];
  var LANGS = [["en", "English"], ["es", "Español"]];

  var model = { version: 1, name: "", intro: {}, thanks: {}, allow_save: false, force_confirm: false, questions: [] };
  var readOnly = true; // browse a published survey read-only until fork/new
  var designer = document.getElementById("designer");
  var form = document.getElementById("survey-form");
  if (!designer || !form) return;

  // ---- helpers ----
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function field(labelText, input) {
    var row = el("div", "form-row");
    var l = el("label", null, labelText);
    if (input.id) l.htmlFor = input.id;
    row.appendChild(l);
    row.appendChild(input);
    return row;
  }
  function input(value, ph, type) {
    var i = el("input");
    i.type = type || "text";
    if (value != null) i.value = value;
    if (ph) i.placeholder = ph;
    return i;
  }
  function bind(i, fn) { i.addEventListener("input", fn); return i; }
  function langInputs(obj, ph, makeTextarea) {
    // returns a fragment of EN/ES inputs writing into obj[lang]
    var wrap = el("div", "lang-grid");
    LANGS.forEach(function (lg) {
      var ctl = makeTextarea ? el("textarea") : input(obj[lg[0]] || "", ph + " (" + lg[0] + ")");
      if (makeTextarea) { ctl.rows = 2; ctl.value = obj[lg[0]] || ""; ctl.placeholder = ph + " (" + lg[0] + ")"; }
      bind(ctl, function () { obj[lg[0]] = ctl.value; });
      wrap.appendChild(ctl);
    });
    return wrap;
  }

  function blankQuestion() {
    return { key: "q" + (model.questions.length + 1), type: "stars", label: {}, required: true, max: 5 };
  }

  // ---- render ----
  function render() {
    designer.textContent = "";

    // Intro / thanks
    var meta = el("div", "card sub");
    meta.appendChild(el("h3", null, "Intro & thank-you"));
    meta.appendChild(field("Intro text", langInputs(model.intro, "Shown above the questions", true)));
    meta.appendChild(field("Thank-you text", langInputs(model.thanks, "Shown after submitting", true)));
    meta.appendChild(saveOptions());
    designer.appendChild(meta);

    model.questions.forEach(function (q, idx) {
      designer.appendChild(questionCard(q, idx));
    });
    syncRaw();
    applyReadOnly();
  }

  // In browse mode, disable every control and hide the per-card buttons so the
  // selected published survey shows read-only (it's immutable; fork to change).
  function applyReadOnly() {
    designer.querySelectorAll("input,select,textarea,button").forEach(function (e) {
      if (e.tagName === "BUTTON") e.style.display = readOnly ? "none" : "";
      else e.disabled = readOnly;
    });
  }

  // Allow Save lets respondents save progress (stored server-side against their
  // link, so they can resume on any device). Force Confirm is a modifier on it,
  // so it only appears once Allow Save is checked.
  function saveOptions() {
    var box = el("div", "save-options");

    var allow = el("label", "inline-check");
    var allowCb = el("input"); allowCb.type = "checkbox"; allowCb.checked = !!model.allow_save;
    allowCb.addEventListener("change", function () {
      model.allow_save = allowCb.checked;
      if (!allowCb.checked) model.force_confirm = false; // meaningless without saving
      render();
    });
    allow.appendChild(allowCb);
    allow.appendChild(document.createTextNode(" Allow Save (respondents can save and resume later)"));
    box.appendChild(allow);

    if (model.allow_save) {
      var force = el("label", "inline-check");
      var forceCb = el("input"); forceCb.type = "checkbox"; forceCb.checked = !!model.force_confirm;
      forceCb.addEventListener("change", function () { model.force_confirm = forceCb.checked; syncRaw(); });
      force.appendChild(forceCb);
      force.appendChild(document.createTextNode(" Force Confirm (ask before saving or submitting)"));
      box.appendChild(force);
    }
    return box;
  }

  function questionCard(q, idx) {
    var c = el("div", "card sub q-card");

    var head = el("div", "q-head");
    var typeSel = el("select");
    TYPES.forEach(function (t) {
      var o = el("option", null, t[1]); o.value = t[0];
      if (t[0] === q.type) o.selected = true;
      typeSel.appendChild(o);
    });
    typeSel.addEventListener("change", function () { q.type = typeSel.value; applyTypeDefaults(q); render(); });
    head.appendChild(el("span", "q-num", "Q" + (idx + 1)));
    head.appendChild(typeSel);
    var spacer = el("span", "q-spacer"); head.appendChild(spacer);
    head.appendChild(iconBtn("↑", "Move up", function () { move(idx, -1); }));
    head.appendChild(iconBtn("↓", "Move down", function () { move(idx, 1); }));
    head.appendChild(iconBtn("✕", "Delete", function () { model.questions.splice(idx, 1); render(); }));
    c.appendChild(head);

    var key = input(q.key, "lowercase key, e.g. csat");
    bind(key, function () { q.key = key.value; syncRaw(); });
    c.appendChild(field("Key (data column)", key));

    c.appendChild(field("Question label", langInputs(q.label, "Question")));

    // type-specific
    c.appendChild(typeFields(q));

    var req = el("label", "inline-check");
    var cb = el("input"); cb.type = "checkbox"; cb.checked = q.required !== false;
    cb.addEventListener("change", function () { q.required = cb.checked; syncRaw(); });
    req.appendChild(cb); req.appendChild(document.createTextNode(" Required"));
    c.appendChild(req);

    return c;
  }

  function typeFields(q) {
    var box = el("div");
    if (q.type === "stars") {
      var mx = input(q.max || 5, "5", "number");
      bind(mx, function () { q.max = parseInt(mx.value, 10) || 5; syncRaw(); });
      box.appendChild(field("Max stars", mx));
    } else if (q.type === "scale") {
      var mn = input(q.min || 1, "1", "number"); bind(mn, function () { q.min = parseInt(mn.value, 10) || 1; syncRaw(); });
      var mx2 = input(q.max || 7, "7", "number"); bind(mx2, function () { q.max = parseInt(mx2.value, 10) || 7; syncRaw(); });
      box.appendChild(field("Min", mn));
      box.appendChild(field("Max", mx2));
      box.appendChild(endsFields(q));
    } else if (q.type === "nps") {
      box.appendChild(el("p", "muted", "Fixed 0–10 scale."));
      box.appendChild(endsFields(q));
    } else if (q.type === "choice" || q.type === "multichoice") {
      box.appendChild(optionsEditor(q));
    } else if (q.type === "text") {
      var ml = input(q.maxlen || 500, "500", "number"); bind(ml, function () { q.maxlen = parseInt(ml.value, 10) || 0; syncRaw(); });
      box.appendChild(field("Max length", ml));
      q.placeholder = q.placeholder || {};
      box.appendChild(field("Placeholder", langInputs(q.placeholder, "Placeholder")));
    }
    return box;
  }

  function endsFields(q) {
    q.ends = q.ends || { low: {}, high: {} };
    q.ends.low = q.ends.low || {}; q.ends.high = q.ends.high || {};
    var box = el("div");
    box.appendChild(field("Low-end label", langInputs(q.ends.low, "e.g. Very hard")));
    box.appendChild(field("High-end label", langInputs(q.ends.high, "e.g. Very easy")));
    return box;
  }

  function optionsEditor(q) {
    q.options = q.options || [];
    var box = el("div", "options");
    box.appendChild(el("label", null, "Options"));
    q.options.forEach(function (opt, i) {
      var row = el("div", "opt-row");
      var val = input(opt.value, "value"); bind(val, function () { opt.value = val.value; syncRaw(); });
      row.appendChild(val);
      opt.label = opt.label || {};
      LANGS.forEach(function (lg) {
        var li = input(opt.label[lg[0]] || "", "label (" + lg[0] + ")");
        bind(li, function () { opt.label[lg[0]] = li.value; syncRaw(); });
        row.appendChild(li);
      });
      row.appendChild(iconBtn("✕", "Remove option", function () { q.options.splice(i, 1); render(); }));
      box.appendChild(row);
    });
    box.appendChild(iconBtn("+ Option", "Add option", function () { q.options.push({ value: "", label: {} }); render(); }, "btn ghost small"));
    return box;
  }

  function iconBtn(text, title, fn, cls) {
    var b = el("button", cls || "icon-btn", text);
    b.type = "button"; b.title = title;
    b.addEventListener("click", fn);
    return b;
  }

  function move(idx, d) {
    var j = idx + d;
    if (j < 0 || j >= model.questions.length) return;
    var tmp = model.questions[idx]; model.questions[idx] = model.questions[j]; model.questions[j] = tmp;
    render();
  }

  function applyTypeDefaults(q) {
    if (q.type === "stars" && !q.max) q.max = 5;
    if (q.type === "scale") { if (!q.min) q.min = 1; if (!q.max) q.max = 7; }
    if ((q.type === "choice" || q.type === "multichoice") && !q.options) q.options = [];
    if (q.type === "text" && !q.maxlen) q.maxlen = 500;
  }

  // ---- serialize ----
  function serialize() { return JSON.stringify(model, null, 2); }
  function syncRaw() {
    var raw = document.getElementById("definition-raw");
    if (raw) raw.value = serialize();
  }

  // ---- init ----
  function load(jsonText) {
    try {
      var d = JSON.parse(jsonText);
      model = { version: d.version || 1, name: d.name || "", intro: d.intro || {}, thanks: d.thanks || {}, allow_save: !!d.allow_save, force_confirm: !!d.force_confirm, questions: d.questions || [] };
    } catch (e) { model = { version: 1, name: "", intro: {}, thanks: {}, allow_save: false, force_confirm: false, questions: [] }; }
    var nm = document.getElementById("survey-name");
    if (nm) nm.value = model.name;
    render();
  }

  // ---- modes ----
  function show(id, on) { var e = document.getElementById(id); if (e) e.style.display = on ? "" : "none"; }
  function enterBrowse() {
    readOnly = true;
    show("browse-actions", true); show("edit-actions", false); show("name-row", false);
    render();
  }
  function enterEdit(prefillName) {
    readOnly = false;
    var nm = document.getElementById("survey-name");
    if (nm) { nm.value = prefillName != null ? prefillName : (model.name || ""); model.name = nm.value; }
    show("browse-actions", false); show("edit-actions", true); show("name-row", true);
    render();
  }

  // ---- wire ----
  document.getElementById("add-question").addEventListener("click", function () {
    model.questions.push(blankQuestion()); render();
  });
  var nameInput = document.getElementById("survey-name");
  if (nameInput) nameInput.addEventListener("input", function () { model.name = nameInput.value; });
  var fromTpl = document.getElementById("new-from-template");
  if (fromTpl) fromTpl.addEventListener("click", function () {
    enterEdit(model.name ? "Copy of " + model.name : ""); // reuse the loaded survey as the starting point
  });
  var blank = document.getElementById("new-blank");
  if (blank) blank.addEventListener("click", function () {
    model = { version: 1, name: "", intro: {}, thanks: {}, allow_save: false, force_confirm: false, questions: [] };
    enterEdit("");
  });
  var cancel = document.getElementById("cancel-edit");
  if (cancel) cancel.addEventListener("click", function () { window.location.reload(); });
  var picker = document.getElementById("surveyPicker");
  if (picker) picker.addEventListener("change", function () {
    window.location.href = "/survey?set=" + encodeURIComponent(picker.value);
  });
  var loadBtn = document.getElementById("load-json");
  if (loadBtn) loadBtn.addEventListener("click", function () {
    load(document.getElementById("definition-raw").value);
  });
  form.addEventListener("submit", function () {
    if (nameInput) model.name = nameInput.value;
    document.getElementById("definition-json").value = serialize();
  });

  // Confirm before deleting a survey (destructive: also removes its responses).
  var delForm = document.getElementById("delete-survey-form");
  if (delForm) delForm.addEventListener("submit", function (e) {
    if (!window.confirm("Delete this survey and ALL of its collected responses? This cannot be undone.")) {
      e.preventDefault();
    }
  });

  // ---- init: browse the selected published survey read-only ----
  load(document.getElementById("definition-raw").value);
  enterBrowse();
})();
