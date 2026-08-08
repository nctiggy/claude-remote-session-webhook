/*
 * crswd.js — the dashboard's only script, and in this milestone it does four
 * things: the digital rain, the pane's live half, spending a submit once, and
 * keeping an open fleet current.
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

  /*
   * What the rain occasionally says (FR-031).
   *
   * Here rather than in a template, and that is the whole of the containment:
   * routing these through the daemon would make them look like content —
   * something this host is telling its operator — on the one surface whose job
   * is saying what is running on it. Nothing on the server knows these strings,
   * so no route can be made to carry one and no render can put one in the
   * document.
   *
   * They are about the theme and never about the fleet, for the same reason. A
   * line that read like status would be a status display with nothing behind
   * it, which is worse than no line at all.
   */
  const MESSAGES = [
    'wake up',
    'follow the white rabbit',
    'knock knock',
    'there is no spoon',
    'free your mind',
  ];

  /*
   * Roughly two seconds of legibility, and roughly one message every two or
   * three minutes per field.
   *
   * Occasional is the requirement rather than a taste: a header that spoke on a
   * schedule would be something to watch, and a background that has to be
   * watched has stopped being a background. Drawn per frame rather than on a
   * timer, so the odds are a property of the loop that is already running.
   */
  const SAYING_FRAMES = 28;
  const SAYING_ODDS = 0.0005;

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
   * The message, drawn into the same grid the columns fall down — one glyph per
   * cell, centred, in the lead glyph's colour, so it reads as the rain having
   * lined up rather than as text laid on top of it. Drawn on the canvas and
   * never inserted: canvas content is not in the accessibility tree, so FR-033
   * holds by construction rather than by an attribute somebody could remove.
   *
   * It is redrawn every frame it is showing and never erased. The translucent
   * wipe at the top of paint is what takes it away — the same fade that gives a
   * column its trail — so a message dissolves back into the field it came out
   * of instead of blinking off.
   *
   * Called from paint and from nowhere else, which is FR-032. paint runs only
   * from the shared loop, and start() does not schedule that loop under a
   * reduced-motion preference; giving the message a timer of its own would have
   * been a second path for it to arrive by on a page that asked for stillness.
   */
  const saying = (field) => {
    if (field.saidFor > 0) {
      field.saidFor -= 1;
    } else if (Math.random() < SAYING_ODDS) {
      field.said = MESSAGES[Math.floor(Math.random() * MESSAGES.length)];
      field.saidFor = SAYING_FRAMES;
    }
    if (field.saidFor === 0) {
      return;
    }

    const { context, cell, said } = field;
    // Clamped at the left edge: a field narrower than the longest message would
    // otherwise start it off-canvas, which loses the front of the line rather
    // than the ends of it.
    const columns = Math.floor(field.canvas.width / cell);
    const from = Math.max(0, Math.floor((columns - said.length) / 2));
    const row = Math.floor(field.canvas.height / cell / 2);

    context.fillStyle = token('--text');
    for (let index = 0; index < said.length; index++) {
      context.fillText(said[index], (from + index) * cell, row * cell);
    }
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

    // Last, so the line sits over the trails rather than under them — and
    // inside paint, so there is exactly one place the rain can speak from.
    saying(field);
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
      // Rebuilt here, so a field that stopped and started again — which is what
      // switching the preference on and back off does — begins silent rather
      // than resuming a line nobody was reading.
      said: '',
      saidFor: 0,
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
   * The submit-once guard (T010, research.md R7).
   *
   * FR-018 — a repeated action must not duplicate its effect — is satisfied by
   * three of the four actions' own semantics: a second destroy finds no record,
   * a second rename is the same end state, and a second compact is a second
   * delivery the operator asked for. The create is the exception. Two rapid
   * submissions are two unsandboxed shells, and no amount of re-reading the
   * response tells them apart afterwards.
   *
   * This is the guard and not the bound. What actually stops a runaway is the
   * concurrent-session cap and the create rate limit, both server-side and both
   * out of a browser's reach; a page with scripting disabled is refused by those
   * exactly as it is today. All this removes is the double-click that would
   * spend one of them by accident.
   *
   * Disabled inside the submit event, which fires after the browser has decided
   * to submit — so a form the browser refused on its own constraint validation
   * is left with a live control, which is the only useful state to leave it in.
   * The control carries no name attribute, so disabling it removes nothing from
   * the entry list the form sends; disabling the fields would.
   *
   * The in-progress state FR-031 asks for is the note the form names, revealed
   * here and written over there. A control that merely greyed out would say the
   * page had stopped rather than that the host was working, and the copy belongs
   * to the template for the reason the pane's ended note does.
   */
  const spendOnce = (form) => {
    form.addEventListener('submit', () => {
      for (const control of form.querySelectorAll('button[type="submit"]')) {
        control.disabled = true;
      }
      const note = document.getElementById(form.dataset.submitOnce);
      if (note) {
        note.hidden = false;
      }
    });
  };

  for (const form of document.querySelectorAll('form[data-submit-once]')) {
    spendOnce(form);
  }

  /*
   * The fleet's live half (US3, issue #15, contracts/fleet-stream.md).
   *
   * The daemon says *what* changed and never *what it now looks like*: one
   * identifier per event, under a name that says which of the three happened
   * (research.md R6). So this listens by name rather than through onmessage —
   * appeared, changed and vanished are three different things to do — and turns
   * each one into a re-fetch of that session's own card. Nothing here is told
   * the name, the state or the working directory, which is what keeps hours of
   * open connection free of session data.
   *
   * What it re-fetches is one card and never the fleet. The address comes off
   * the page (dashboard.html) with the daemon's own route parameter still in it,
   * because this file is served to every page and knows about no session and no
   * route; the same reason the pane reads its stream address off the element it
   * updates.
   *
   * It used to answer the two events that change the fleet's *shape* with a
   * reload, because a card arriving at an empty page and the last card leaving
   * are the page's own composition. That reload cost everything on the page
   * that was not in the markup: a half-typed working directory in the create
   * form, the scroll position, the caret, and any message the page was showing
   * — which is why the action toast had to be given sessionStorage to survive
   * the very reload the action caused (issue #42, issue #51).
   *
   * So the page now renders both of its shapes and hides the one that does not
   * apply (dashboard.html), and what happens here is a reveal rather than a
   * composition. The distinction is the whole of why this is not a second fleet
   * page: the empty state, the summary row and every count in it are the
   * daemon's own markup, composed once per render, and this file chooses which
   * of two authored shapes is on screen. It never writes a sentence, a row or a
   * number the server did not.
   *
   * The reload is kept for what remains genuinely uncomposable here — a card
   * this page could not fetch, an answer that did not contain the card it named,
   * and a state the summary has no row for. It is the fallback rather than the
   * default, which is the correction issue #51 asks for.
   *
   * Nothing here animates. FR-022 is answered by the stylesheet's universal
   * `transition: none` under a reduced-motion preference rather than by a rule
   * remembered here; a card is exchanged for its successor in one operation with
   * no intermediate state for anything to fade between, and a card that arrives
   * or leaves does so in one too. Nothing here moves focus either, which is the
   * point of not reloading: an operator typing a working directory when another
   * session ends keeps their caret.
   */
  const watchFleet = (shell) => {
    const stalled = document.getElementById(shell.dataset.fleetStalled);
    const announcement = document.getElementById(shell.dataset.fleetChanged);
    const grid = () => shell.querySelector('.grid');
    const summary = () => shell.querySelector('.summary');
    const blank = () => shell.querySelector('.empty');
    const cardFor = (id) => shell.querySelector(`article.card[data-session="${CSS.escape(id)}"]`);

    /*
     * FR-020, and the reason it is revealed and never hidden again: the page
     * has missed a window it cannot ask about afterwards. EventSource reconnects
     * on its own, which is the right recovery — a fresh request, authorised from
     * scratch, re-subscribed — but the changes that happened while it was gone
     * arrive as no event at all, so a note that hid itself on reconnection would
     * be the page claiming a currency it never got back. Only a reload does
     * that, and the copy over there says so.
     */
    const lostTheFleet = () => {
      if (stalled) {
        stalled.hidden = false;
      }
    };

    let reloading = false;
    const recompose = () => {
      if (!reloading) {
        reloading = true;
        location.reload();
      }
    };

    /*
     * The summary row is derived from the cards below it, which is the rule the
     * page states about itself — so this re-derives it from the cards that are
     * there now rather than adding or subtracting one. It reads each row's own
     * pill for the state it counts, so no state is named in this file: the two
     * the daemon derives today and any the status component renders later are
     * the same code path.
     *
     * It answers whether it could account for every card, and the caller reloads
     * when it could not. A card in a state this render composed no row for is
     * the one thing that would make the summary a second source of truth — a row
     * that under-reports says the fleet is smaller than the grid beneath it
     * already shows. Nothing produces that today, which is exactly when to
     * decide what happens: needs-auth arrives with the device-code relay, and
     * the honest answer to a state this page has no row for is the render that
     * has one.
     */
    const recount = () => {
      const fleet = grid();
      if (!fleet) {
        return true;
      }

      const rows = new Map();
      for (const row of shell.querySelectorAll('.summary-state')) {
        const pill = row.querySelector('.pill');
        const count = row.querySelector('.summary-count');
        if (!pill || !count) {
          continue;
        }
        const state = Array.from(pill.classList).find((name) => name.startsWith('pill-'));
        if (state) {
          rows.set(state, count);
        }
      }

      for (const pill of fleet.querySelectorAll('.pill')) {
        const state = Array.from(pill.classList).find((name) => name.startsWith('pill-'));
        if (state && !rows.has(state)) {
          return false;
        }
      }

      for (const [state, count] of rows) {
        count.textContent = fleet.getElementsByClassName(state).length;
      }
      return true;
    };

    /*
     * Which of the two shapes the daemon rendered is on screen (FR-021).
     *
     * Both are in the document from the first render, so this is three `hidden`
     * attributes and no markup: the grid and its summary while there is
     * something to show, the empty state while there is not. The case a naive
     * version misses is the second transition — the first card returning — and
     * it is the same line here, which is why it is one function called from both
     * paths rather than a branch in each.
     */
    const compose = () => {
      const fleet = grid();
      const nothing = !fleet || fleet.childElementCount === 0;
      for (const shape of [fleet, summary()]) {
        if (shape) {
          shape.hidden = nothing;
        }
      }
      const explanation = blank();
      if (explanation) {
        explanation.hidden = !nothing;
      }
    };

    /*
     * What changed, for an operator who cannot see it change (issue #51).
     *
     * A reload re-announced the whole page as a side effect of throwing it away.
     * Updating in place keeps everything that reload destroyed and owes that
     * announcement deliberately, or a page that silently rearranges is worse for
     * a non-sighted operator than one that reloads.
     *
     * The sentence is the page's, read off the region it is written into with
     * the count substituted the way the card's address takes an identifier. Only
     * the two events that change what is on the page are announced: a card
     * replaced in place is docs/components.md's noise, and narrating it would be
     * the whole grid turned into a live region.
     */
    const say = (what) => {
      const fleet = grid();
      if (!announcement || !fleet) {
        return;
      }
      const sentence = announcement.dataset[what];
      if (sentence) {
        announcement.textContent = sentence.replace('{n}', fleet.childElementCount);
      }
    };

    const drop = (id) => {
      const here = cardFor(id);
      if (!here) {
        return;
      }
      here.remove();
      if (!recount()) {
        recompose();
        return;
      }
      compose();
      say('fleetVanished');
    };

    /*
     * One card, re-fetched from the daemon rather than assembled here.
     *
     * The markup is the daemon's own — the same card template the page was
     * rendered from — and it is parsed into an inert document before one element
     * of it is taken. A parsed document runs nothing: no script in it executes,
     * no image in it loads, and what is imported is the single <article> this
     * event named. That matters because the answer is a whole page and a session
     * page carries a pane, which is everything an unsandboxed program printed —
     * it is escaped by html/template on the way out and it is left in the
     * document nobody adopts, which is the one place it can do nothing at all.
     *
     * The request carries the operator's ambient credential and nothing else,
     * exactly as the click that opens that page would; a card is authorised by
     * the identity the daemon verifies, so the answer is the uniform not-found
     * when the session is gone or was never this operator's. Both mean the same
     * thing to a page: the card goes.
     *
     * Anything else that goes wrong reveals the note. A page that could not
     * re-fetch a card it has been told changed is a page showing a card it
     * cannot vouch for, which is FR-020 about one card instead of the fleet.
     *
     * The ticket is the ordering. Two changes to one session in quick
     * succession are two requests, and nothing makes them answer in the order
     * they were sent — so a response that is no longer the newest one for its
     * session is dropped rather than painted over a fresher card.
     */
    const newest = new Map();
    let issued = 0;

    const refresh = (id) => {
      // The grid is on every render of this page now, hidden while the fleet is
      // empty rather than absent, so this is the fallback and no longer the
      // ordinary answer to a session appearing.
      if (!grid()) {
        recompose();
        return;
      }

      const ticket = (issued += 1);
      newest.set(id, ticket);

      fetch(shell.dataset.fleetCard.replace('{id}', encodeURIComponent(id)), {
        credentials: 'same-origin',
      })
        .then((answer) => {
          if (answer.status === 404) {
            return null;
          }
          if (!answer.ok) {
            throw new Error('the daemon did not answer with the card');
          }
          return answer.text();
        })
        .then((markup) => {
          if (newest.get(id) !== ticket) {
            return;
          }
          // Nothing outstanding for this session can be newer than the answer
          // being applied, so the ticket is spent rather than kept: an entry per
          // identifier this page ever saw would grow for as long as the tab is
          // open, and one that is gone refuses a slower response just as well.
          newest.delete(id);

          if (markup === null) {
            drop(id);
            return;
          }

          const fetched = new DOMParser()
            .parseFromString(markup, 'text/html')
            .querySelector(`article.card[data-session="${CSS.escape(id)}"]`);
          if (!fetched) {
            lostTheFleet();
            return;
          }

          const card = document.importNode(fetched, true);
          const here = cardFor(id);
          const fleet = grid();
          if (here) {
            // A card exchanged for its successor: the fleet holds what it held,
            // so there is no shape to compose and nothing to announce.
            here.replaceWith(card);
            if (!recount()) {
              recompose();
            }
            return;
          }
          if (!fleet) {
            // A page with no grid at all is not one this file composed, so the
            // render that would have it is the honest answer.
            recompose();
            return;
          }

          // Appended, because the fleet is oldest first — the order Store.List
          // imposes and the page keeps (dashboard.go) — and a session that has
          // just appeared is the newest thing in it. The card carries no
          // sortable age to insert by: what it renders is coarse prose for a
          // person to read, so the position is derived from what the event
          // means rather than from a string parsed back out of the markup.
          fleet.append(card);
          if (!recount()) {
            recompose();
            return;
          }
          compose();
          say('fleetAppeared');
        })
        .catch(lostTheFleet);
    };

    const live = new EventSource(shell.dataset.fleetStream);

    // Decoded before the page is touched, on the same terms the pane decodes a
    // screen: an event that could not be read leaves the fleet exactly as it is
    // rather than acting on a fragment of itself.
    const idOf = (event) => JSON.parse(event.data).id;

    live.addEventListener('appeared', (event) => refresh(idOf(event)));
    live.addEventListener('changed', (event) => refresh(idOf(event)));
    live.addEventListener('vanished', (event) => drop(idOf(event)));

    /*
     * Every ending of this stream arrives here and none of them arrives as an
     * event. The daemon names three and not one of them is a farewell
     * (contracts/fleet-stream.md), so a shutdown, a restart, a severed
     * connection and a subscriber this daemon dropped for falling behind are all
     * one thing to a browser: a response that ended. Which is exactly why the
     * page has a sentence for it.
     */
    live.onerror = lostTheFleet;
  };

  for (const shell of document.querySelectorAll('main[data-fleet-stream]')) {
    watchFleet(shell);
  }
})();

/*
 * The action toast (issue #42).
 *
 * The four action forms are real forms posting to real routes, and that is what
 * makes them work with scripting off and what makes their submit buttons
 * keyboard-operable without anything being added. But a form post navigates, and
 * a navigation throws this page away to show one sentence somewhere else.
 *
 * So this posts them instead and writes the answer into the live region the page
 * already carries. Nothing here is required for the daemon to be correct — every
 * check that matters ran server-side before the answer existed — and a browser
 * that never runs this file gets the floor T014 put under it: a 303 back to the
 * fleet with the same sentence rendered as a banner (FR-024). This is the
 * enhancement over that, not the thing that makes the actions work.
 *
 * What the fetch reads is therefore a whole fleet page, because the routes
 * answer 303 and fetch follows it. The sentence is pulled out of the banner that
 * page rendered, so the daemon's own fixed copy is what an operator is told
 * whichever path they came down — there is one vocabulary (outcome.go) rather
 * than one for the scripted half and one for the other.
 *
 * The answer is read as text, never inserted as markup. The banner is
 * daemon-authored today, so innerHTML would be safe today; textContent is what
 * keeps it safe after someone makes one of them carry a name or a path, which is
 * the same lesson docs/components.md was corrected for twice.
 */
(() => {
  const toast = document.getElementById('action-toast');
  if (!toast) {
    return;
  }

  /*
   * The answer has to outlive a reload.
   *
   * The fleet's live half reloads this page whenever the fleet's *shape* changes
   * — which a destroy and a create always do. So the first version of this toast
   * appeared and was wiped by the reload a moment later: the operator saw a
   * flash and could not read it. The message is the one thing on the page that
   * must survive the very event it is reporting.
   *
   * sessionStorage rather than a query parameter: it is per-tab, it never
   * reaches the daemon, and it cannot be linked to someone. A message is the
   * browser's own business here — it was already rendered once on this tab.
   */
  const pending = 'crswd.outcome';

  let hide;
  const paint = (message) => {
    toast.textContent = message;
    toast.hidden = false;
    clearTimeout(hide);
    // Long enough to read a sentence, and cleared on the next action so two
    // clicks never leave the earlier answer standing under the later one.
    hide = setTimeout(() => {
      toast.hidden = true;
      try {
        sessionStorage.removeItem(pending);
      } catch {
        // A tab with storage refused still gets the toast, just not across a
        // reload. Losing the message is better than losing the action.
      }
    }, 6000);
  };

  const show = (message) => {
    // Stored before it is painted, because the reload can arrive between the
    // fetch resolving and the next frame.
    try {
      sessionStorage.setItem(pending, message);
    } catch {
      // See above: storage is the enhancement, the toast is the feature.
    }
    paint(message);
  };

  // Whatever the reload interrupted, said again on the page it landed on.
  try {
    const carried = sessionStorage.getItem(pending);
    if (carried) {
      paint(carried);
    }
  } catch {
    // Nothing to restore is the ordinary case; a tab that refuses storage is
    // indistinguishable from one that had nothing waiting.
  }

  /*
   * The answer's text, without trusting it to be markup.
   *
   * DOMParser builds an inert document: no script runs, no image loads, nothing
   * in it reaches this page. Taking textContent from that is the same reading a
   * browser would give the sentence, with none of the consequences of letting it
   * be one.
   */
  const sentence = (html) => {
    const parsed = new DOMParser().parseFromString(html, 'text/html');

    /*
     * The banner, not the page.
     *
     * What arrives here is the fleet the redirect landed on (T014), so the one
     * thing worth reading out of it is the outcome banner it rendered — the same
     * sentence, from the same closed vocabulary, that a scriptless operator sees
     * on that page. Everything else on it is the fleet, which the operator is
     * looking at already.
     *
     * The alarming outcome is a titled block rather than a line, and both halves
     * are said: reducing "Teardown could not be verified" to its body is exactly
     * the flattening FR-023 forbids, and this region is the only place a scripted
     * operator is told at all.
     *
     * Anything else is treated as having no message rather than as a message. A
     * toast is a place for one line; if a route ever answers with something
     * larger, saying nothing is better than reading a page aloud — the mistake
     * that put an entire card in here when a create answered with one (#78).
     */
    const alarm = parsed.querySelector('.outcome-alarm');
    if (alarm) {
      const heading = (alarm.querySelector('.outcome-heading')?.textContent || '').trim();
      const body = (alarm.querySelector('.outcome-body')?.textContent || '').trim();
      return [heading, body].filter(Boolean).join('. ');
    }

    const outcome = parsed.querySelector('.outcome');
    if (outcome) {
      return (outcome.textContent || '').trim();
    }

    return '';
  };

  /*
   * Delegated from the document, not attached per form.
   *
   * The first version bound a listener to every action form at load, and the
   * fleet's live half replaces cards whenever the stream says one changed. A
   * replaced card is a new form element, which never had the listener — so the
   * first rename after any fleet change navigated to a bare fragment, exactly
   * what this exists to prevent. A listener on the document sees a form that did
   * not exist when the page loaded, which is the only kind this page has after
   * its first update.
   */
  document.addEventListener('submit', async (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !form.getAttribute('action')?.startsWith('/dashboard/')) {
      return;
    }
    // Let the browser do the ordinary thing if it cannot do this one.
    if (typeof window.fetch !== 'function') {
      return;
    }
    event.preventDefault();

    const reenable = () => {
      for (const control of form.querySelectorAll('button[type="submit"]')) {
        control.disabled = false;
      }
      const note = document.getElementById(form.dataset.submitOnce);
      if (note) {
        note.hidden = true;
      }
    };

    try {
      const answer = await fetch(form.action, {
        method: 'POST',
        body: new URLSearchParams(new FormData(form)),
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        // The gate reads Sec-Fetch-Site, which the browser sets and script
        // cannot. same-origin keeps this a same-origin request so it stays set
        // to the one value the daemon admits.
        credentials: 'same-origin',
      });
      show(sentence(await answer.text()) || 'The host answered without a message.');
      reenable();
      if (form.matches('.create-form')) {
        form.reset();
      }
    } catch {
      // A request that never reached the host is the one case where the operator
      // must not be told anything happened, because nothing did.
      show('That action could not be sent. The fleet is unchanged.');
      reenable();
    }
  });
})();

/*
 * The working-directory picker's theme (T010, contracts/themed-combobox.md),
 * and FR-045's sentence, which now lives inside it.
 *
 * The control is markup and stays markup. `<input list>` and a `<datalist>`
 * filter as the operator types, take a keyboard, announce their options and
 * leave any path typeable in full with nothing running at all — five of the six
 * picker requirements are the platform's own, and this file is not what makes
 * any of them true (FR-015, FR-043). What it adds is the one thing no
 * stylesheet can reach: a datalist popup is drawn by the browser and no CSS in
 * any engine styles it, so the picker was the single control on this dashboard
 * wearing the browser's appearance rather than this interface's. Milestone 4's
 * R6 chose the native control and was right about everything it weighed; this
 * is the part it missed, and the answer is to enhance that control rather than
 * to replace it.
 *
 * So the property to keep is the one the abandoned branch lost — 225 lines
 * reimplementing filtering, focus and ARIA the platform already had, which
 * degraded to nothing with scripting off. Everything below runs over a field
 * that already works: with this file absent the operator meets exactly the
 * control the daemon rendered, and nothing here narrows what may be typed or
 * what may be submitted (FR-008).
 *
 * The first two statements are an order, and it is load-bearing twice over.
 * `field.list` *is* the element the `list` attribute names — the daemon's own
 * options, read rather than copied, so there is no second list here to disagree
 * with the markup — and `removeAttribute` is precisely what makes it null. Read
 * first, then cut: the other order leaves the enhancement with nothing to offer
 * and the operator with a themed box that is permanently empty. Cutting at all
 * is what stops the browser opening its own popup over this one, which is two
 * lists saying the same thing in two appearances.
 *
 * The ARIA is added here and never in the template, for the reason the template
 * says at the point it declines to carry it: with no script there is no
 * combobox to expand and no listbox to control, and markup that describes a
 * control which is not there is worse for a screen reader than markup
 * describing the plain field that is. Every attribute below is added by the
 * code that makes it true, and `aria-controls` is read off the listbox rather
 * than spelled here, so the id stays the template's to rename.
 *
 * The sentence is the template's too, filled here, exactly as it was when it
 * had a region of its own. It has moved into `.combo-status` — the picker's own
 * aside, beside the control it is about — so the field carries one live region
 * rather than two with one of them dead.
 */
(() => {
  'use strict';

  /*
   * How long the typing has to stop before the sentence is rewritten. A polite
   * live region queues rather than interrupts, so a note written on every
   * keystroke hands a screen reader a backlog of counts to read out after the
   * operator has finished — each one already wrong by the time it is spoken.
   *
   * The list itself is not delayed by it. What is drawn is looked at rather
   * than listened to, and a list that lagged the typing by four hundred
   * milliseconds would be a control that felt broken to the operator it is
   * fastest for.
   */
  const SETTLE_MS = 400;

  const enhance = (combo) => {
    const field = combo.querySelector('input');
    const listbox = combo.querySelector('.combo-list');
    const status = combo.querySelector('.combo-status');
    if (!field || !listbox || !status) {
      return;
    }

    // Read before the join is cut, and the guard is FR-018a rather than
    // defensiveness: a daemon with nothing to suggest renders no list at all,
    // and a combobox over no options would be this file announcing a control
    // with nothing behind it.
    const offered = field.list;
    if (!offered) {
      return;
    }

    field.removeAttribute('list');

    field.setAttribute('role', 'combobox');
    field.setAttribute('aria-expanded', 'false');
    field.setAttribute('aria-autocomplete', 'list');
    field.setAttribute('aria-controls', listbox.id);
    listbox.setAttribute('role', 'listbox');

    /*
     * The rule the engines filter by — a case-insensitive substring of the
     * option's own value — so what is drawn here is what the popup this
     * replaces would have shown. Read off the options the daemon rendered every
     * time rather than out of an array built once, which is the same reason the
     * summary row re-derives its counts from the cards below it.
     */
    const matching = () => {
      const typed = field.value.trim().toLowerCase();
      const paths = [];
      for (const option of offered.options) {
        if (typed === '' || option.value.toLowerCase().includes(typed)) {
          paths.push(option.value);
        }
      }
      return paths;
    };

    // Which option the keyboard is on, as a position in the list that is drawn
    // right now: -1 is none, which is what every filter starts at.
    let active = -1;

    /*
     * The listbox, rebuilt from what matches. textContent and never innerHTML:
     * a suggestion is a directory name off a filesystem walk, so it is a string
     * this file has no business turning into markup — the rule docs/security.md
     * sets for the pane, applied to the one other place this file writes what
     * the host said.
     *
     * `hidden` carries whether it is open, and `aria-expanded` says the same
     * thing to a reader who cannot see it. Nothing to show closes it: a bordered
     * empty box under the field reads as a control that has broken, and a
     * combobox claiming to be expanded over no options is that same lie told
     * twice.
     *
     * Each option is given an id (T011) because `aria-activedescendant` names
     * one element by id and can point at nothing else. It is built from the
     * listbox's own id rather than spelled here, for the reason `aria-controls`
     * is read off the same property: the id belongs to the template, and this
     * file holds no copy of it to drift.
     */
    const draw = (paths) => {
      listbox.replaceChildren(
        ...paths.map((path, index) => {
          const option = document.createElement('li');
          option.id = `${listbox.id}-option-${index}`;
          option.setAttribute('role', 'option');
          option.textContent = path;
          return option;
        }),
      );

      // Every option here is new markup, so whatever the keyboard was on went
      // with the previous filter. Cleared rather than kept by position: an index
      // that survived a keystroke would point at a different path, which is a
      // control moving the answer while the operator types it.
      active = -1;
      field.removeAttribute('aria-activedescendant');

      const open = paths.length > 0;
      listbox.hidden = !open;
      field.setAttribute('aria-expanded', open ? 'true' : 'false');
    };

    /*
     * Silent while nothing is filtered. FR-045 is about a list showing a
     * subset; a region that also said "showing all of them" would be narrating
     * the operator's own typing back at them, which is the noise
     * docs/components.md keeps off the grid and the pane.
     */
    const say = (showing) => {
      const sentence = status.dataset.workdirSubset;
      if (!sentence) {
        return;
      }
      const offering = offered.options.length;
      status.textContent = showing === offering
        ? ''
        : sentence.replace('{n}', showing).replace('{all}', offering);
    };

    let settling;

    field.addEventListener('input', () => {
      const paths = matching();
      draw(paths);
      clearTimeout(settling);
      settling = setTimeout(() => say(paths.length), SETTLE_MS);
    });

    /*
     * The keyboard (T011, and the table in contracts/themed-combobox.md).
     *
     * The rule the whole picker turns on bites hardest here. This is a text
     * field and it stays one: nothing below reads a character that was typed,
     * nothing below rewrites what is in the field except the one accept the
     * operator asked for, and the four keys that are answered are answered
     * because each of them means something to a list that is open. Everything
     * else reaches the field untouched, which is what keeps any path typeable in
     * full (FR-008) — the list offers, it does not narrow what may be sent.
     */
    const optionAt = (index) => listbox.children[index] || null;

    /*
     * Where the keyboard is, said three ways because three different things read
     * it. `aria-activedescendant` is what tells a reader where the focus that
     * never left the input is now pointing; `aria-selected` is the selector the
     * ring in crswd.css is keyed on, because `:focus-visible` can never reach an
     * option and without the attribute a keyboard operator moves an invisible
     * cursor; and the scroll is for the operator who can see one — the list is a
     * bounded scroll box, so an option moved past its edge is one they have no
     * way to know they are on.
     */
    const activate = (index) => {
      const leaving = optionAt(active);
      if (leaving) {
        leaving.removeAttribute('aria-selected');
      }
      active = index;

      const option = optionAt(active);
      if (!option) {
        field.removeAttribute('aria-activedescendant');
        return;
      }
      option.setAttribute('aria-selected', 'true');
      field.setAttribute('aria-activedescendant', option.id);
      option.scrollIntoView({ block: 'nearest' });
    };

    /*
     * Closing, which is the path the keyboard adds: until these keys existed the
     * list shut only when nothing matched what was typed, so a list opened by
     * one keystroke stayed open over the rest of the form.
     *
     * The subset sentence goes with it, including the one a settling timer was
     * about to write. `.combo-status` counts what the list is showing, and a
     * count left standing under a closed list is a sentence about something that
     * is no longer there.
     */
    const close = () => {
      activate(-1);
      listbox.hidden = true;
      field.setAttribute('aria-expanded', 'false');
      clearTimeout(settling);
      status.textContent = '';
    };

    /*
     * Wrapping, because the alternative is a key that does nothing at an end the
     * operator cannot see. The first press lands on the near end of the list in
     * the direction it was pressed, which is what makes ↓ the way in.
     */
    const step = (by) => {
      const count = listbox.children.length;
      if (count === 0) {
        return;
      }
      activate(active < 0 ? (by > 0 ? 0 : count - 1) : (active + by + count) % count);
    };

    field.addEventListener('keydown', (event) => {
      switch (event.key) {
        case 'ArrowDown':
        case 'ArrowUp':
          // Reopened here rather than only on the next keystroke: Escape closes
          // a list the operator may not be finished with, and without this the
          // only way back to the options is to change what is typed.
          if (listbox.hidden) {
            draw(matching());
          }
          step(event.key === 'ArrowDown' ? 1 : -1);
          // Refused, because both keys already mean "the caret goes to the end
          // of the value" in a text field, and an operator moving through paths
          // would be dragging the caret with them.
          event.preventDefault();
          break;

        case 'Enter': {
          const option = optionAt(active);
          // Enter with nothing active is the submit this form has always had,
          // and it is deliberately untouched: a path typed in full is sent by
          // the same key whether or not this file ran. Only the accept is
          // claimed, and only when there is something to accept.
          if (!option) {
            return;
          }
          accept(option);
          event.preventDefault();
          break;
        }

        case 'Escape':
          // What was typed stays typed. Escape dismisses the offer and never the
          // answer — no path through here touches the field's value — and it is
          // refused so that a browser which reads Escape in a text field as
          // "revert what was typed" does not do that instead.
          if (listbox.hidden) {
            return;
          }
          close();
          event.preventDefault();
          break;

        case 'Tab':
          // Never refused: Tab is how the operator leaves this field, and
          // whatever is in it stands exactly as it would with this file absent.
          // The list is shut on the way out so it cannot hang over the control
          // that now has focus.
          close();
          break;

        default:
          // Typing, which is never intercepted.
          break;
      }
    });

    /*
     * The accept, which two things now ask for and which is written once (T017).
     *
     * It sits between its callers because it belongs to neither: the key above
     * and the pointer below take the same option and put the same text in the
     * field, and a second assignment would be a second answer to what the
     * operator chose — on the one field where any path has to stay typeable in
     * full (FR-008), which is a property held by there being exactly one write
     * in this file and by that write reading an option the daemon offered.
     */
    const accept = (option) => {
      field.value = option.textContent;
      close();
    };

    /*
     * The pointer (T017), which is the one thing this enhancement took away
     * rather than added: the popup it replaces was clickable, the themed list
     * was not, and .combo-list li has drawn `cursor: pointer` since T009 over a
     * control that did nothing.
     *
     * Bound on mousedown and never on click. The blur below shuts the list, and
     * a blur lands between the press and the click that would have followed it,
     * so a click handler here would only ever select an option when it won a
     * race — a selection that works most of the time on a fast machine and
     * reads as flakiness rather than as a bug. That is why the two are one
     * change.
     *
     * The press is refused for every position inside the list rather than only
     * one that lands on an option, because the default action of a press on
     * something that cannot hold focus is to take focus off the field: dragging
     * this box's scroll bar would otherwise blur the field and shut the list
     * under the pointer. Refusing it leaves focus where the operator left it,
     * which is also where aria-activedescendant has been telling a reader it is.
     *
     * Delegated from the list for the reason the toast is delegated from the
     * document: every option is new markup on every keystroke, so a handler
     * bound to one when this ran is a handler on an element that is gone.
     *
     * The option is a <li> with a listener and not a <button>, which is not the
     * thing docs/components.md's floor forbids. Focus stays on the input for as
     * long as this control is open — a focusable option would take it out of
     * the field being typed in — and what makes the option operable without a
     * pointer is the keyboard above, which is what that rule is about.
     */
    listbox.addEventListener('mousedown', (event) => {
      event.preventDefault();

      const option = event.target instanceof Element ? event.target.closest('li') : null;
      if (!option) {
        return;
      }
      // The same two steps in the same order Enter runs them: what was pointed
      // at becomes the active option first, so the ring, the reader and the
      // value all name the one that was taken.
      activate([...listbox.children].indexOf(option));
      accept(option);
    });

    /*
     * And the close that has to be written with it (T017). Escape, Tab and
     * Enter all shut the list; a pointer put anywhere else on the page shut
     * nothing, so the one way out of this control that an operator using a
     * mouse actually takes was the one that left a list hanging over the rest
     * of the form.
     *
     * The same `close` the keyboard runs, and nothing here touches what was
     * typed. A blur that normalised the field to the nearest option is the
     * shape FR-008 refuses — the list would have become the allowlist, quietly,
     * on the path an operator never watches.
     */
    field.addEventListener('blur', () => {
      close();
    });
  };

  for (const combo of document.querySelectorAll('[data-combo]')) {
    enhance(combo);
  }
})();

/*
 * A click that ends a text selection is not a click on a link (T028, FR-051).
 *
 * The card's readable half is one anchor now (#60), so the 32 characters a card
 * renders precisely so they can be copied sit *inside* a link. Dragging across
 * them already yields a selection rather than a link drag — that is what
 * draggable="false" on the anchor buys (session-card.html) — but releasing at
 * the end of that drag is still a click on a link, so the browser navigates and
 * the operator is on another page holding text they cannot see the source of.
 *
 * This is a papercut and the fix stays one. It is a single preventDefault on the
 * click that *ends* a selection, and that is the whole of what this block does:
 * nothing here navigates. With this file absent the anchor is exactly the link
 * the template rendered and a click on it opens the session — which is the
 * property to keep rather than the behaviour. A card that needed a script to be
 * clickable would have turned FR-051's convenience into a dependency, on the one
 * page US3 spent a milestone making work with nothing running.
 *
 * Delegated from the document rather than attached per anchor, for the reason
 * the toast above is: the fleet's live half replaces a card whenever the stream
 * says that session changed, and a replaced card is a new anchor that never had
 * a listener. Every card on this page is one of those after its first update.
 *
 * The selection has to be inside *this* anchor. A collapsed one is an ordinary
 * click — a mousedown collapses whatever was selected before it, so a plain
 * click on a card arrives here with nothing selected — and a selection anywhere
 * else on the page is somebody's earlier reading rather than the drag that just
 * ended here. Both navigate, because both are what an operator asked for.
 *
 * `detail` is the keyboard's exemption, and it is not politeness. Enter on a
 * focused link fires a click with no pointer behind it (detail === 0). A
 * selection sitting inside the anchor — left by Shift+arrow, or by a drag that
 * ended a moment ago — would otherwise make that card unreachable by keyboard
 * until something collapsed it, trading a papercut for the accessibility floor
 * docs/components.md sets.
 */
(() => {
  'use strict';

  document.addEventListener('click', (event) => {
    if (event.detail === 0) {
      return;
    }
    const link = event.target instanceof Element ? event.target.closest('a.card-link') : null;
    if (!link) {
      return;
    }

    const selection = window.getSelection();
    if (!selection || selection.isCollapsed || selection.rangeCount === 0) {
      return;
    }
    if (link.contains(selection.getRangeAt(0).commonAncestorContainer)) {
      event.preventDefault();
    }
  });
})();
