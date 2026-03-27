/**
 * RLAAS Docs — Navigation controller
 *
 * Desktop: hover to open, stays open while mouse is inside,
 *          closes on mouseleave (with small grace delay) or
 *          when another dropdown opens.
 * Mobile:  tap/click to toggle (hover events ignored).
 * Escape key closes all dropdowns.
 */
(function () {
  'use strict';

  var CLOSE_DELAY = 120; // ms grace period before closing on mouseleave
  var items = document.querySelectorAll('.nav-links > li');

  function closeAll() {
    items.forEach(function (li) { li.classList.remove('dd-open'); });
  }

  function isMobile() {
    return window.innerWidth <= 768;
  }

  items.forEach(function (li) {
    var toggle = li.querySelector('.nav-dropdown-toggle');
    if (!toggle) return;

    var timer = null;

    function open() {
      clearTimeout(timer);
      // close siblings first
      items.forEach(function (other) {
        if (other !== li) other.classList.remove('dd-open');
      });
      li.classList.add('dd-open');
    }

    function scheduleClose() {
      timer = setTimeout(function () {
        li.classList.remove('dd-open');
      }, CLOSE_DELAY);
    }

    function cancelClose() {
      clearTimeout(timer);
    }

    /* ── Desktop: hover ────────────────────────────────────── */
    li.addEventListener('mouseenter', function () {
      if (!isMobile()) open();
    });
    li.addEventListener('mouseleave', function () {
      if (!isMobile()) scheduleClose();
    });

    /* ── Mobile: click/tap toggle ──────────────────────────── */
    toggle.addEventListener('click', function (e) {
      e.preventDefault();
      e.stopPropagation();
      if (li.classList.contains('dd-open')) {
        li.classList.remove('dd-open');
      } else {
        open();
      }
    });
  });

  /* ── click outside closes all (mobile & desktop) ─────────── */
  document.addEventListener('click', function (e) {
    if (!e.target.closest('.nav-links')) closeAll();
  });

  /* ── hamburger (mobile) ──────────────────────────────────── */
  var hamburger = document.querySelector('.nav-toggle');
  if (hamburger) {
    hamburger.addEventListener('click', function () {
      document.querySelector('.nav-links').classList.toggle('open');
    });
  }

  /* ── Escape key closes all ─────────────────────────────── */
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeAll();
  });
})();
