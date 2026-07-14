// Confirmation for destructive form submits.
//
// This replaces inline onsubmit="return confirm(...)" handlers, which the CSP
// (script-src 'self', no 'unsafe-inline') blocks outright — meaning they never
// ran and destructive actions went through unconfirmed. Any form carrying a
// data-confirm message now asks first.
(function () {
  "use strict";
  document.querySelectorAll("form[data-confirm]").forEach(function (form) {
    form.addEventListener("submit", function (e) {
      if (!window.confirm(form.getAttribute("data-confirm"))) e.preventDefault();
    });
  });
})();
