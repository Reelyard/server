// Reelyard website: theme toggle, copy buttons, install tabs, docs table-of-contents highlight.
(function () {
  var root = document.documentElement;
  function get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }
  function set(k, v) { try { localStorage.setItem(k, v); } catch (e) {} }

  // Theme: follows the system until the visitor picks one.
  var saved = get("reelyard-theme");
  if (saved === "light" || saved === "dark") root.setAttribute("data-theme", saved);
  function isDark() {
    var t = root.getAttribute("data-theme");
    if (t) return t === "dark";
    return window.matchMedia && matchMedia("(prefers-color-scheme: dark)").matches;
  }
  document.querySelectorAll(".theme-btn").forEach(function (b) {
    b.addEventListener("click", function () {
      var next = isDark() ? "light" : "dark";
      root.setAttribute("data-theme", next);
      set("reelyard-theme", next);
    });
  });

  // Copy buttons on code blocks.
  document.querySelectorAll("pre").forEach(function (pre) {
    var btn = document.createElement("button");
    btn.className = "copy";
    btn.type = "button";
    btn.textContent = "Copy";
    btn.addEventListener("click", function () {
      var text = pre.querySelector("code") ? pre.querySelector("code").innerText : pre.innerText;
      navigator.clipboard.writeText(text.replace(/\n$/, "")).then(function () {
        btn.textContent = "Copied";
        setTimeout(function () { btn.textContent = "Copy"; }, 1500);
      });
    });
    pre.appendChild(btn);
  });

  // Tabs.
  document.querySelectorAll("[data-tabs]").forEach(function (group) {
    var buttons = group.querySelectorAll("[role=tab]");
    buttons.forEach(function (b) {
      b.addEventListener("click", function () {
        buttons.forEach(function (o) {
          o.setAttribute("aria-selected", o === b ? "true" : "false");
          document.getElementById(o.getAttribute("aria-controls")).hidden = o !== b;
        });
      });
    });
  });

  // Docs: highlight the section being read.
  var links = document.querySelectorAll(".toc a[href^='#']");
  if (links.length && "IntersectionObserver" in window) {
    var byId = {};
    links.forEach(function (a) { byId[a.getAttribute("href").slice(1)] = a; });
    var visible = {};
    var obs = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) { visible[e.target.id] = e.isIntersecting; });
      var heads = document.querySelectorAll("article h2[id]");
      for (var i = 0; i < heads.length; i++) {
        if (visible[heads[i].id]) {
          links.forEach(function (a) { a.classList.remove("active"); });
          if (byId[heads[i].id]) byId[heads[i].id].classList.add("active");
          break;
        }
      }
    }, { rootMargin: "-80px 0px -65% 0px" });
    document.querySelectorAll("article h2[id]").forEach(function (h) { obs.observe(h); });
  }

  // Docs: "#" links next to headings.
  document.querySelectorAll("article h2[id], article h3[id]").forEach(function (h) {
    var a = document.createElement("a");
    a.className = "anchor";
    a.href = "#" + h.id;
    a.setAttribute("aria-label", "Link to this section");
    a.textContent = "#";
    h.appendChild(a);
  });
})();
