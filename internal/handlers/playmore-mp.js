/*!
 * playmore-mp.js — PlayMore multiplayer lobby client for games.
 *
 * Drop this into an HTML5 game uploaded to PlayMore to get online
 * multiplayer via the platform's lobby + message relay. The game runs
 * in a sandboxed iframe and talks to the PlayMore page (its parent)
 * over window.postMessage; this shim wraps that protocol in a small
 * callback API so games never hand-write the plumbing.
 *
 *   <script src="/playmore-mp.js"></script>
 *   <script>
 *     PlayMore.onReady(function (ctx) {
 *       // ctx = { code, gameId, you:{id,username}, host:bool, players:[...], sessionToken }
 *       // ctx.sessionToken is a short-lived pm_gs_ bearer token for game-scoped
 *       // API calls (e.g. POST /api/v1/games/:id/play-sessions). It expires in
 *       // 5 minutes — the SPA refreshes it; do not cache.
 *     });
 *     PlayMore.onMessage(function (from, data) {  ...  });
 *     PlayMore.onPlayers(function (players) {  ...  });
 *     PlayMore.onClosed(function () {  ...  });
 *     // later:
 *     PlayMore.send({ move: 'e4' });        // broadcast to the lobby
 *     PlayMore.send(state, somePlayerId);   // send to one player
 *   </script>
 *
 * There is no network code and no secrets here — the platform owns the
 * WebSocket. Full protocol + limits: <base>/docs and docs/DEVELOPER.md.
 */
(function () {
  'use strict';

  if (window.PlayMore) return; // idempotent if included twice

  var parent = window.parent;
  // parentOrigin is learned from the first message the platform sends
  // us (the "reply only to the origin that contacted you" pattern).
  // Until then we only ever post the payload-free 'ready' handshake,
  // for which '*' is safe. After the handshake every outbound frame is
  // targeted at the platform's real origin, so a malicious sibling
  // frame or a re-embedding page can't intercept game traffic.
  var parentOrigin = null;

  var ctx = { code: '', gameId: '', you: null, host: false, players: [] };
  var started = false;

  var handlers = {
    ready: [],   // (ctx)
    message: [], // (from, data)
    players: [], // (players)
    closed: []   // ()
  };

  function on(kind) {
    return function (fn) {
      if (typeof fn === 'function') handlers[kind].push(fn);
      return API;
    };
  }

  function emit(kind, a, b) {
    var list = handlers[kind];
    for (var i = 0; i < list.length; i++) {
      try { list[i](a, b); } catch (e) { /* a game callback threw; keep going */ }
    }
  }

  function post(obj, origin) {
    try { parent.postMessage(obj, origin || '*'); } catch (e) { /* parent gone */ }
  }

  window.addEventListener('message', function (ev) {
    // Only trust the embedding PlayMore page. The game may host its own
    // iframes; ignore anything that isn't from our parent window.
    if (ev.source !== parent) return;
    var d = ev.data;
    if (!d || typeof d !== 'object' || !d.playmore) return;

    // Pin the platform origin from the first legitimate frame.
    if (parentOrigin === null) parentOrigin = ev.origin;

    switch (d.playmore) {
      case 'init':
        ctx = {
          code: d.code || '',
          gameId: d.game_id || '',
          you: d.you || null,
          host: !!d.host,
          players: d.players || [],
          sessionToken: d.session_token || ''
        };
        started = true;
        emit('ready', ctx);
        break;
      case 'players':
        ctx.players = d.players || [];
        emit('players', ctx.players);
        break;
      case 'msg':
        emit('message', d.from, d.data);
        break;
      case 'closed':
        started = false;
        emit('closed');
        break;
    }
  });

  var API = {
    /* Register callbacks (chainable). */
    onReady: on('ready'),
    onMessage: on('message'),
    onPlayers: on('players'),
    onClosed: on('closed'),

    /* Relay `data` (any JSON value) to the lobby. Omit `to` to
     * broadcast to every other player; pass a player id to unicast. */
    send: function (data, to) {
      if (!started || parentOrigin === null) return API; // not in a live lobby yet
      post({ playmore: 'send', to: to || '', data: data }, parentOrigin);
      return API;
    },

    /* Current lobby snapshot accessors. */
    players: function () { return ctx.players.slice(); },
    me: function () { return ctx.you; },
    isHost: function () { return ctx.host; },
    code: function () { return ctx.code; },
    gameId: function () { return ctx.gameId; },
    sessionToken: function () { return ctx.sessionToken; },
    isActive: function () { return started; }
  };

  window.PlayMore = API;

  // Announce readiness. If the game script runs before this shim (script
  // order), the platform still has our listener; if after, onReady was
  // already registered. Either way the handshake reply drives onReady.
  post({ playmore: 'ready' });
})();
