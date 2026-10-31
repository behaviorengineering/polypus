// Theme switch (auto, light, dark) and the "On this page" highlight.
(function () {
  var root = document.documentElement;
  var media = matchMedia("(prefers-color-scheme: dark)");
  var order = ["auto", "light", "dark"];

  function saved() {
    try { return localStorage.getItem("theme") || "auto"; } catch (e) { return "auto"; }
  }

  function apply(mode) {
    var dark = mode === "dark" || (mode === "auto" && media.matches);
    root.dataset.theme = dark ? "dark" : "light";
    document.querySelectorAll(".theme-toggle").forEach(function (b) {
      var name = mode.charAt(0).toUpperCase() + mode.slice(1);
      b.setAttribute("aria-label", "Colour theme: " + name);
      b.querySelector(".theme-toggle__label").textContent = name;
    });
  }

  apply(saved());
  media.addEventListener("change", function () { apply(saved()); });
  document.querySelectorAll(".theme-toggle").forEach(function (b) {
    b.addEventListener("click", function () {
      var next = order[(order.indexOf(saved()) + 1) % order.length];
      try { localStorage.setItem("theme", next); } catch (e) {}
      apply(next);
    });
  });

  // Narrow screens start with the page list folded.
  var box = document.querySelector(".navbox");
  if (box && matchMedia("(max-width: 820px)").matches) box.open = false;

  var links = Array.prototype.slice.call(document.querySelectorAll(".docs__toc a[href^='#']"));
  if (!links.length || !("IntersectionObserver" in window)) return;
  var byId = {};
  links.forEach(function (a) { byId[decodeURIComponent(a.hash.slice(1))] = a; });
  var heads = Object.keys(byId).map(function (id) { return document.getElementById(id); }).filter(Boolean);
  var observer = new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      if (!e.isIntersecting) return;
      links.forEach(function (a) { a.classList.remove("is-active"); });
      byId[e.target.id].classList.add("is-active");
    });
  }, { rootMargin: "-70px 0px -70% 0px" });
  heads.forEach(function (h) { observer.observe(h); });
})();
