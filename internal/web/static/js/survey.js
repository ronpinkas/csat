// Public survey form enhancements:
//
//   1. Conditional gating — fieldsets tagged with data-showif-key/-vals are shown
//      only when the controlling question matches. Hidden fields lose `required`
//      and are cleared, so native validation and the (authoritative) server-side
//      gate agree. The server re-checks everything regardless.
//
//   2. Draft persistence — two modes, and only ever ONE source of truth:
//      • allow_save OFF: the draft lives in localStorage (per device).
//      • allow_save ON:  the draft lives on the SERVER, keyed to the link. It is
//        rendered into the form by the server, autosaved in the background, and
//        localStorage is not used at all — that's what lets someone resume on a
//        different device.
//
//   3. Save vs submit — with allow_save the button reads "Save progress" until
//      every visible required question is answered, then becomes "Submit". With
//      force_confirm, the action asks first instead of acting immediately.
(function () {
  "use strict";
  var form = document.querySelector("form.survey, form");
  if (!form) return;

  var allowSave = form.getAttribute("data-allow-save") === "1";
  var forceConfirm = form.getAttribute("data-force-confirm") === "1";
  var saveUrl = form.getAttribute("data-save-url") || "";
  var labelSubmit = form.getAttribute("data-label-submit") || "";
  var labelSave = form.getAttribute("data-label-save") || "";
  var msgSaving = form.getAttribute("data-msg-saving") || "";
  var msgSaved = form.getAttribute("data-msg-saved") || "";
  var msgFailed = form.getAttribute("data-msg-failed") || "";

  var btn = document.getElementById("survey-submit");
  var statusEl = document.getElementById("save-status");
  var modal = document.getElementById("confirm-modal");

  function skip(name) { return !name || name === "csrf" || name === "set" || name === "action"; }

  // ---- conditional gating ----
  var gated = Array.prototype.slice.call(form.querySelectorAll("[data-showif-key]"));

  function controllerValues(key) {
    var out = [];
    // Keys are restricted to [a-z][a-z0-9_]* so this selector is always safe.
    form.querySelectorAll('[name="' + key + '"]').forEach(function (el) {
      if (el.type === "radio" || el.type === "checkbox") {
        if (el.checked) out.push(el.value);
      } else if (el.value != null && el.value !== "") {
        out.push(el.value);
      }
    });
    return out;
  }

  function wantValues(fs) {
    try { return JSON.parse(fs.getAttribute("data-showif-vals") || "[]"); }
    catch (e) { return []; }
  }

  function met(fs) {
    var have = controllerValues(fs.getAttribute("data-showif-key"));
    var want = wantValues(fs);
    for (var i = 0; i < have.length; i++) {
      if (want.indexOf(have[i]) !== -1) return true;
    }
    return false;
  }

  function applyGating() {
    gated.forEach(function (fs) {
      var show = met(fs);
      fs.hidden = !show;
      fs.querySelectorAll("input,select,textarea").forEach(function (el) {
        if (show) {
          if (el.dataset.wasRequired === "1") el.setAttribute("required", "");
        } else {
          if (el.hasAttribute("required")) {
            el.dataset.wasRequired = "1";
            el.removeAttribute("required");
          }
          // Drop stale answers so a hidden field is never submitted.
          if (el.type === "radio" || el.type === "checkbox") el.checked = false;
          else el.value = "";
        }
      });
    });
  }

  // ---- local draft (only when the server isn't holding one) ----
  var token = new URLSearchParams(window.location.search).get("t") || "";
  var DRAFT_KEY = "csat:draft:" + token;

  function serialize() {
    var data = {};
    form.querySelectorAll("input,select,textarea").forEach(function (el) {
      if (skip(el.name)) return;
      if (el.type === "radio") {
        if (el.checked) data[el.name] = el.value;
      } else if (el.type === "checkbox") {
        if (!data[el.name]) data[el.name] = [];
        if (el.checked) data[el.name].push(el.value);
      } else {
        data[el.name] = el.value;
      }
    });
    return data;
  }

  function restore(data) {
    if (!data) return;
    form.querySelectorAll("input,select,textarea").forEach(function (el) {
      if (skip(el.name) || !(el.name in data)) return;
      var v = data[el.name];
      if (el.type === "radio") el.checked = el.value === v;
      else if (el.type === "checkbox") el.checked = Array.isArray(v) && v.indexOf(el.value) !== -1;
      else el.value = v;
    });
  }

  function saveLocal() {
    try { localStorage.setItem(DRAFT_KEY, JSON.stringify(serialize())); } catch (e) { /* ignore */ }
  }
  function loadLocal() {
    try { var s = localStorage.getItem(DRAFT_KEY); return s ? JSON.parse(s) : null; } catch (e) { return null; }
  }
  function clearLocal() {
    try { localStorage.removeItem(DRAFT_KEY); } catch (e) { /* ignore */ }
  }

  // ---- server draft (allow_save) ----
  var saveTimer = null, saving = false;

  // "Complete" == every visible required question is answered. Gating already
  // stripped `required` from hidden fields, so the browser answers this for us.
  function isComplete() { return form.checkValidity(); }

  function updateLabel() {
    if (!allowSave || !btn) return;
    btn.textContent = isComplete() ? labelSubmit : labelSave;
  }

  function setStatus(msg, cls) {
    if (!statusEl) return;
    statusEl.textContent = msg || "";
    statusEl.className = "save-status" + (cls ? " " + cls : "");
  }

  function saveToServer(done) {
    if (!allowSave || !saveUrl || saving) return;
    saving = true;
    setStatus(msgSaving, "");
    var body = new URLSearchParams(new FormData(form));
    body.set("action", "save");
    fetch(saveUrl, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
    }).then(function (r) {
      saving = false;
      if (!r.ok) throw new Error(String(r.status));
      setStatus(msgSaved, "ok");
      if (done) done(true);
    }).catch(function () {
      saving = false;
      setStatus(msgFailed, "err"); // never fail silently — the work is the point
      if (done) done(false);
    });
  }

  function queueAutosave() {
    if (!allowSave) return;
    clearTimeout(saveTimer);
    saveTimer = setTimeout(function () { saveToServer(); }, 1500);
  }

  function doSubmit() {
    clearTimeout(saveTimer);
    var action = document.getElementById("form-action");
    if (action) action.value = "submit";
    clearLocal();
    form.submit(); // bypasses the submit listener, so no recursion
  }

  // ---- force-confirm modal ----
  function openModal() {
    if (!modal) return;
    var subBtn = document.getElementById("confirm-submit");
    if (subBtn) subBtn.hidden = !isComplete(); // Submit only offered once complete
    modal.hidden = false;
  }
  function closeModal() { if (modal) modal.hidden = true; }

  if (modal) {
    document.getElementById("confirm-cancel").addEventListener("click", closeModal);
    document.getElementById("confirm-save").addEventListener("click", function () {
      closeModal();
      saveToServer();
    });
    document.getElementById("confirm-submit").addEventListener("click", function () {
      closeModal();
      doSubmit();
    });
    modal.addEventListener("click", function (e) { if (e.target === modal) closeModal(); });
  }

  // ---- submit flow ----
  form.addEventListener("submit", function (e) {
    if (!allowSave) {
      clearLocal(); // classic behaviour: native validation already passed
      return;
    }
    // allow_save forms are novalidate — we decide save vs submit ourselves.
    e.preventDefault();
    if (forceConfirm) { openModal(); return; }
    if (isComplete()) { doSubmit(); return; }
    saveToServer();
  });

  // ---- init ----
  // With allow_save the server already rendered the draft into the form, and it
  // wins over anything cached locally. Without it, restore the local copy.
  if (!allowSave) restore(loadLocal());
  applyGating();
  updateLabel();

  function onEdit() {
    applyGating();
    updateLabel();
    if (allowSave) queueAutosave();
    else saveLocal();
  }
  form.addEventListener("input", onEdit);
  form.addEventListener("change", onEdit);
})();
