// Public survey form enhancements. The base form works without JavaScript; this
// script adds two progressive enhancements:
//
//   1. Conditional gating — fieldsets tagged with data-showif-key/-vals are
//      shown only when the controlling question's answer matches. Hidden fields
//      have their `required` removed and values cleared, so native validation
//      and the (authoritative) server-side gate agree. The server re-checks
//      everything regardless; this is purely UX.
//   2. Draft autosave — the in-progress form is mirrored to localStorage, keyed
//      by the link token, and restored on load so an executive can complete the
//      survey over several sittings. Cleared on submit. Client-only, per-device.
(function () {
  "use strict";
  var form = document.querySelector("form.survey, form");
  if (!form) return;

  // Ignore framework/security fields when persisting.
  function skip(name) {
    return !name || name === "csrf" || name === "set";
  }

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
    try {
      return JSON.parse(fs.getAttribute("data-showif-vals") || "[]");
    } catch (e) {
      return [];
    }
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

  // ---- draft persistence (localStorage, keyed by link token) ----
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
      if (el.type === "radio") {
        el.checked = el.value === v;
      } else if (el.type === "checkbox") {
        el.checked = Array.isArray(v) && v.indexOf(el.value) !== -1;
      } else {
        el.value = v;
      }
    });
  }

  function saveDraft() {
    try {
      localStorage.setItem(DRAFT_KEY, JSON.stringify(serialize()));
    } catch (e) {
      /* storage full or unavailable — ignore */
    }
  }

  function loadDraft() {
    try {
      var s = localStorage.getItem(DRAFT_KEY);
      return s ? JSON.parse(s) : null;
    } catch (e) {
      return null;
    }
  }

  function clearDraft() {
    try {
      localStorage.removeItem(DRAFT_KEY);
    } catch (e) {
      /* ignore */
    }
  }

  // ---- init ----
  // Server-rendered prefill is already in the DOM; overlay any saved draft
  // (in-progress edits win), then reconcile conditional visibility.
  restore(loadDraft());
  applyGating();

  form.addEventListener("input", function () {
    applyGating();
    saveDraft();
  });
  form.addEventListener("change", function () {
    applyGating();
    saveDraft();
  });
  form.addEventListener("submit", function () {
    clearDraft();
  });
})();
