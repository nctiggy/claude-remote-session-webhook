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
   * There are two changes it deliberately will not make, and both are the
   * page's own composition: a card arriving when the page rendered the empty
   * state, and the last card leaving. The fleet page chooses between the
   * summary-and-grid and the empty state, and a script that made that choice a
   * second time would be a second fleet page — with a second empty state, a
   * second summary row and two places for either to be wrong. It reloads
   * instead, which is the server composing the page again, and that is the
   * whole of what a reload costs on a page whose only unsaved state is a create
   * form the operator has not submitted.
   *
   * Nothing here animates. FR-022 is answered by the stylesheet's universal
   * `transition: none` under a reduced-motion preference rather than by a rule
   * remembered here, and a card is exchanged for its successor in one operation
   * with no intermediate state for anything to fade between.
   */
  const watchFleet = (shell) => {
    const stalled = document.getElementById(shell.dataset.fleetStalled);
    const grid = () => shell.querySelector('.grid');
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
     */
    const recount = () => {
      const fleet = grid();
      if (!fleet) {
        return;
      }
      for (const row of shell.querySelectorAll('.summary-state')) {
        const pill = row.querySelector('.pill');
        const count = row.querySelector('.summary-count');
        if (!pill || !count) {
          continue;
        }
        const state = Array.from(pill.classList).find((name) => name.startsWith('pill-'));
        if (state) {
          count.textContent = fleet.getElementsByClassName(state).length;
        }
      }
    };

    const drop = (id) => {
      const here = cardFor(id);
      if (!here) {
        return;
      }
      here.remove();
      const fleet = grid();
      if (!fleet || fleet.childElementCount === 0) {
        recompose();
        return;
      }
      recount();
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
            here.replaceWith(card);
          } else if (fleet) {
            fleet.append(card);
          } else {
            // The fleet emptied while this request was in flight, so the page is
            // the empty state now and composing one is not this file's to do.
            recompose();
            return;
          }
          recount();
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
 * keyboard-operable without anything being added. But a form post navigates: the
 * browser replaced the fleet with the handler's answer, which is one sentence
 * with no page around it. An operator clicked Compact and got a white page.
 *
 * So this posts them instead and writes the answer into the live region the page
 * already carries. Nothing here is required for the daemon to be correct — every
 * check that matters ran server-side before the answer existed — and a browser
 * that never runs this file still gets the old behaviour rather than none.
 *
 * The answer is read as text, never inserted as markup. These fragments are
 * daemon-authored today, so innerHTML would be safe today; textContent is what
 * keeps it safe after someone makes one of them carry a name or a path, which is
 * the same lesson docs/components.md was corrected for twice.
 */
(() => {
  const toast = document.getElementById('action-toast');
  if (!toast) {
    return;
  }

  let hide;
  const show = (message) => {
    toast.textContent = message;
    toast.hidden = false;
    clearTimeout(hide);
    // Long enough to read a sentence, and cleared on the next action so two
    // clicks never leave the earlier answer standing under the later one.
    hide = setTimeout(() => {
      toast.hidden = true;
    }, 6000);
  };

  /*
   * A fragment's text, without trusting it to be markup.
   *
   * DOMParser builds an inert document: no script runs, no image loads, nothing
   * in it reaches this page. Taking textContent from that is the same reading a
   * browser would give the sentence, with none of the consequences of letting it
   * be one.
   */
  const sentence = (html) => {
    const parsed = new DOMParser().parseFromString(html, 'text/html');
    return (parsed.body.textContent || '').trim();
  };

  for (const form of document.querySelectorAll('form[action^="/dashboard/"]')) {
    form.addEventListener('submit', async (event) => {
      // Let the browser do the ordinary thing if it cannot do this one.
      if (typeof window.fetch !== 'function') {
        return;
      }
      event.preventDefault();

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

        // Re-enable the control the submit-once guard spent. The guard exists
        // for the create's double-submit, and with no navigation there is no new
        // page to bring a fresh one back.
        for (const control of form.querySelectorAll('button[type="submit"]')) {
          control.disabled = false;
        }
        const note = document.getElementById(form.dataset.submitOnce);
        if (note) {
          note.hidden = true;
        }
        if (form.matches('.create-form')) {
          form.reset();
        }
      } catch {
        // A request that never reached the host is the one case where the
        // operator must not be told anything happened, because nothing did.
        show('That action could not be sent. The fleet is unchanged.');
        for (const control of form.querySelectorAll('button[type="submit"]')) {
          control.disabled = false;
        }
      }
    });
  }
})();
