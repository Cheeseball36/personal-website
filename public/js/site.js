// Soft light that follows the mouse cursor.
// Skipped on touch screens and when the visitor asks for reduced motion.
(function () {
  var glow = document.querySelector(".glow");
  if (!glow) return;

  var canHover = window.matchMedia("(hover: hover) and (pointer: fine)").matches;
  var reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!canHover || reduced) return;

  var x = 0;
  var y = 0;
  var queued = false;

  function paint() {
    glow.style.setProperty("--x", x + "px");
    glow.style.setProperty("--y", y + "px");
    glow.classList.add("on");
    queued = false;
  }

  window.addEventListener("pointermove", function (e) {
    x = e.clientX;
    y = e.clientY;
    if (!queued) {
      queued = true;
      requestAnimationFrame(paint);
    }
  });

  document.addEventListener("mouseleave", function () {
    glow.classList.remove("on");
  });
})();
