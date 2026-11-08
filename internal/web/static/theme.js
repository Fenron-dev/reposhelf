// SPDX-License-Identifier: GPL-3.0-or-later
// Applies the stored theme before the first paint (T-9.3).
(function () {
  try {
    var t = localStorage.getItem("rs-theme");
    if (!t && window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches) t = "light";
    if (t) document.documentElement.setAttribute("data-theme", t);
    var a = localStorage.getItem("rs-accent");
    if (a) document.documentElement.setAttribute("data-accent", a);
  } catch (e) {}
})();
