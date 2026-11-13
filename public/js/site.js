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
  var inside = false; // is the mouse currently over the page?
  var queued = false;

  function paint() {
    queued = false;
    if (!inside) return; // the mouse left before this frame ran
    glow.style.setProperty("--x", x + "px");
    glow.style.setProperty("--y", y + "px");
    glow.classList.add("on");
  }

  function turnOff() {
    inside = false;
    glow.classList.remove("on");
  }

  window.addEventListener("pointermove", function (e) {
    if (e.pointerType !== "mouse") return; // ignore fingers and pens
    x = e.clientX;
    y = e.clientY;
    inside = true;
    if (!queued) {
      queued = true;
      requestAnimationFrame(paint);
    }
  });

  document.documentElement.addEventListener("pointerleave", turnOff);
  window.addEventListener("blur", turnOff);
})();
