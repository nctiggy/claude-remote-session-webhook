/*
 * crswd.js — the dashboard's only script, and in this milestone it does two
 * things: the digital rain, and the pane's live half.
 *
 * It exists as a file rather than as markup because docs/security.md's policy is
 * sent with no `unsafe-inline` and no exception: a <script> body would be refused
 * by the browser, so neither half below can live in a template. The page loads
 * this with `defer`, so the document is parsed by the time anything here runs.
 *
 * Every value it draws with is read from the stylesheet's own tokens rather than
 * written here. docs/design-system.md's first non-negotiable is that a colour, a
 * size or a font not in that document does not exist, and a canvas is the one
 * surface that can quietly acquire one — there is no CSS to sweep once a fill
 * style is a string in a script. crswd.css is the single source; this file is a
 * reader of it.
 *
 * The second half is the one to be careful in. Everything a Claude session
 * prints arrives in this file and is put into the document by it, so the single
 * assignment below is the browser half of the project's only XSS surface. It is
 * textContent and never innerHTML — a string assigned to textContent has no path
 * to becoming markup, which is what docs/security.md means by closed by
 * construction rather than by sanitising.
 */
(() => {
  'use strict';

  /*
   * Katakana and digits, per the design system. Half-width forms are deliberately
   * absent: a column advances by one cell, so a glyph of a different width would
   * make the field ragged rather than varied.
   */
  const GLYPHS = 'アイウエオカキクケコサシスセソタチツテトナニヌネノハヒフヘホマミムメモヤユヨラリルレロワヲン0123456789';

  /*
   * ~14fps. Rain is a texture rather than an animation — at the display's own
   * rate it reads as noise and costs a repaint of every canvas on the page for
   * as long as the tab is open, which is not what a background is worth.
   */
  const FRAME_MS = 1000 / 14;

  const still = window.matchMedia('(prefers-reduced-motion: reduce)');

  let fields = [];
  let frame = 0;
  let painted = 0;

  const token = (name) =>
    getComputedStyle(document.documentElement).getPropertyValue(name).trim();

  const glyph = () => GLYPHS[Math.floor(Math.random() * GLYPHS.length)];

  /*
   * A canvas's backing store is in pixels and its box is in CSS, so the two are
   * resized together — and only when they differ, because assigning width or
   * height clears the canvas and resets the context, which would wipe the trail
   * every frame.
   */
  const measure = (field) => {
    const width = field.canvas.clientWidth;
    const height = field.canvas.clientHeight;
    if (width === field.canvas.width && height === field.canvas.height) {
      return;
    }

    field.canvas.width = width;
    field.canvas.height = height;
    field.cell = parseFloat(token('--fs-body'));
    field.context.font = `${field.cell}px ${token('--mono')}`;
    field.context.textBaseline = 'top';

    // Each column starts at its own height, so the field arrives already falling
    // rather than as one line sweeping down the page.
    const columns = Math.ceil(width / field.cell);
    field.drops = Array.from({ length: columns }, () =>
      Math.floor((Math.random() * -height) / field.cell),
    );
  };

  /*
   * Wiped with a translucent fill rather than clearRect: the trail behind each
   * lead glyph is the effect, and clearing would leave a field of unrelated
   * characters blinking.
   */
  const paint = (field) => {
    const { context, cell } = field;
    context.fillStyle = token('--rain-wipe');
    context.fillRect(0, 0, field.canvas.width, field.canvas.height);

    for (let column = 0; column < field.drops.length; column++) {
      const x = column * cell;
      const y = field.drops[column] * cell;

      // The lead glyph is brighter than its trail, which is what gives a column
      // a direction.
      context.fillStyle = token('--text');
      context.fillText(glyph(), x, y);
      context.fillStyle = token('--phosphor');
      context.fillText(glyph(), x, y - cell);

      // Columns restart at staggered moments rather than together.
      field.drops[column] =
        y > field.canvas.height && Math.random() > 0.975 ? 0 : field.drops[column] + 1;
    }
  };

  /*
   * One requestAnimationFrame loop across every .rain canvas on the page, not one
   * per canvas: two loops would be two independent repaint schedules for one
   * page, and the browser throttles this one to nothing when the tab is hidden.
   */
  const tick = (now) => {
    frame = requestAnimationFrame(tick);
    if (now - painted < FRAME_MS) {
      return;
    }
    painted = now;

    for (const field of fields) {
      measure(field);
      if (field.canvas.width > 0 && field.canvas.height > 0) {
        paint(field);
      }
    }
  };

  const start = () => {
    if (frame || still.matches) {
      return;
    }
    fields = Array.from(document.querySelectorAll('canvas.rain'), (canvas) => ({
      canvas,
      context: canvas.getContext('2d'),
      cell: 0,
      drops: [],
    })).filter((field) => field.context);
    if (fields.length > 0) {
      frame = requestAnimationFrame(tick);
    }
  };

  const stop = () => {
    cancelAnimationFrame(frame);
    frame = 0;
    fields = [];
  };

  /*
   * A reduced-motion preference removes the rain entirely rather than slowing it,
   * and the stylesheet has already taken the canvases out of the layout. Stopping
   * here as well is the half that matters: a loop drawing into a display:none
   * canvas is still a loop, and the preference is about the machine's work as much
   * as about the page's motion. It is watched rather than read once because it can
   * change while the page is open.
   */
  still.addEventListener('change', () => (still.matches ? stop() : start()));
  start();

  /*
   * The pane's live half. One EventSource per pane the page rendered a hook on,
   * reading the address off the element rather than building one here: this file
   * is served to every page and knows about no session, and the pane it should
   * watch is a fact about the page it is on (web/templates/partials/pane.html).
   *
   * Each event carries the whole current screen as one JSON string, and the
   * whole of what is done with it is the assignment below (contracts/stream.md).
   * There is no accumulator and no append: a Claude session is a full-screen
   * program that repaints in place, so successive captures are redraws rather
   * than new lines, and a transcript stitched out of them would carry a torn line
   * from every cursor move and every spinner (FR-031a).
   *
   * Nothing is done on error. A connection that drops is left to EventSource's
   * own reconnect, which opens a new request that is authorised from scratch —
   * re-connection is never a resumed privilege. What is *not* left to it is the
   * session ending: the daemon says so with a named event, and this closes on
   * it. Without the close, EventSource would reconnect for as long as the tab
   * lives, and every one of those requests is answered with the uniform 404 —
   * a polite client turned into a scanner of its own daemon.
   */
  const watch = (pane) => {
    const live = new EventSource(pane.dataset.stream);
    live.onmessage = (event) => {
      // Decoded before the screen is touched, so an event that could not be read
      // leaves the last good screen where it is rather than replacing it with a
      // fragment of itself.
      const screen = JSON.parse(event.data);

      /*
       * FR-032: an update must never move the reader's place in the screen. The
       * pane is its own scroll container, and replacing its content empties it
       * for an instant — long enough for the browser to clamp the offset against
       * a box with nothing in it, which lands the operator back at the top of a
       * screen they had scrolled through. Read here, put back below, both axes,
       * because the pane scrolls in both.
       */
      const top = pane.scrollTop;
      const left = pane.scrollLeft;

      // The one assignment. JSON.parse yields a string and a string assigned to
      // textContent is text — there is no innerHTML, no insertAdjacentHTML and
      // no htmx swap anywhere near this element, because each of those would
      // insert what an unsandboxed program printed as markup (FR-028, SC-004).
      pane.textContent = screen;

      pane.scrollTop = top;
      pane.scrollLeft = left;
    };

    /*
     * The end of the session, which the daemon sends as the one named event on
     * this stream and which is therefore not something a session can announce by
     * printing it — every screen arrives unnamed, whatever it contains.
     *
     * The screen is left exactly where it is: the last thing the session printed
     * is the most useful thing on the page once it has ended, and replacing it
     * with a sentence would throw that away. The note beside it is what says so
     * (FR-033), revealed rather than written here, because the copy belongs to
     * the template that renders it.
     */
    live.addEventListener('end', () => {
      const note = document.getElementById(pane.dataset.ended);
      if (note) {
        note.hidden = false;
      }
      live.close();
    });

    /*
     * A stream that was refused, rather than one that ended.
     *
     * EventSource reports a dropped connection and a refused one through the same
     * handler, and the difference is readyState: CONNECTING means it will retry
     * on its own, which is the case this deliberately leaves alone. CLOSED means
     * it has given up — the daemon answered something that is not a stream, and
     * the browser will not ask again. That happens for every refusal the contract
     * defines: 429 past CRSW_MAX_STREAMS, 404 on a reload after the session was
     * reaped, 500 when the write deadline could not be lifted.
     *
     * Saying so is the whole point. The failure mode this repairs is the one
     * FR-033 already argues about the *ended* case: a pane that simply stops
     * updating looks exactly like a session sitting quietly at a prompt. An
     * operator who believes that about a session which is in fact running — and
     * running with --dangerously-skip-permissions — has been misled by the one
     * surface whose entire job is telling them what is on their host.
     *
     * The ended note wins if it is already showing: a session that ended and then
     * failed to reconnect ended, and that is the more useful of the two sentences.
     */
    live.onerror = () => {
      if (live.readyState !== EventSource.CLOSED) {
        return;
      }
      const ended = document.getElementById(pane.dataset.ended);
      if (ended && !ended.hidden) {
        return;
      }
      const note = document.getElementById(pane.dataset.stalled);
      if (note) {
        note.hidden = false;
      }
    };
  };

  for (const pane of document.querySelectorAll('pre.pane[data-stream]')) {
    watch(pane);
  }

  /*
   * The fleet's live half (FR-017a). The page rendered once and then described
   * the host forever after, so a session created or reaped since the load was
   * invisible until somebody reloaded by hand — a dashboard quietly showing a
   * session the reaper destroyed twenty minutes ago (#15).
   *
   * It reloads rather than swapping a fragment in. This milestone ships no htmx
   * (contracts/dashboard.md) and the two ways a script has of putting markup on a
   * page are the assignment docs/security.md forbids on this file — the same file
   * everything a Claude session prints arrives in. A reload re-runs GET / through
   * the same owner-scoped read and the same components, so a refreshed fleet
   * cannot describe a session differently from the loaded one (FR-017b), cannot
   * advance an idle clock (FR-017c) and cannot reach a mutating path, because the
   * browser door serves GET only.
   *
   * The interval is read off the page rather than written here, like the pane's
   * stream address above: this file is served to every page and the daemon is the
   * one that decides how stale a document of its own may be. A page carrying no
   * hook refreshes not at all, which is how the single-session view keeps the
   * stream it already has instead of tearing it down every interval.
   */
  const shell = document.querySelector('[data-refresh]');
  const seconds = shell ? Number(shell.dataset.refresh) : 0;
  if (Number.isFinite(seconds) && seconds > 0) {
    /*
     * A timeout rather than an interval, because a reload starts the page — and
     * this timer — again. An interval would be a second schedule that only ever
     * fires once.
     */
    setTimeout(() => {
      /*
       * FR-017d. A hidden tab is not a dashboard anybody is reading, and
       * refreshing one costs the daemon a render and the operator's trail a
       * record (FR-016) to correct a page nobody is looking at. The reload is
       * deferred to the moment it becomes visible, which is the moment a stale
       * fleet would otherwise be read — the only moment the guarantee is about.
       *
       * `once` is what keeps that a single listener: the next visibility change
       * after this one was registered is the tab coming back, and after it the
       * page is being replaced anyway.
       */
      if (document.visibilityState !== 'visible') {
        document.addEventListener('visibilitychange', () => location.reload(), {
          once: true,
        });
        return;
      }
      location.reload();
    }, seconds * 1000);
  }
})();
