/*!
 * playmore-mp.js — PlayMore multiplayer lobby client for games.
 *
 * Drop this into an HTML5 game uploaded to PlayMore to get online
 * multiplayer via the platform's lobby + WebRTC relay. The game runs
 * in a sandboxed iframe and talks to the PlayMore page (its parent)
 * over window.postMessage; this shim wraps that protocol in a small
 * callback API so games never hand-write the plumbing.
 *
 *   <script src="/playmore-mp.js"></script>
 *   <script>
 *     PlayMore.onReady(function (ctx) {
 *       // ctx = { code, gameId, you:{id,username}, host:bool, players:[...], sessionToken }
 *     });
 *     PlayMore.onMessage(function (from, data) {  ...  });
 *     PlayMore.onPlayers(function (players) {  ...  });
 *     PlayMore.onClosed(function () {  ...  });
 *     // later:
 *     PlayMore.send({ move: 'e4' });        // broadcast to the lobby
 *     PlayMore.send(state, somePlayerId);   // send to one player
 *     PlayMore.transport(peerId);           // 'webrtc' or 'relay'
 *     PlayMore.stats();                     // { sent: N, received: N, peers: {...} }
 *   </script>
 *
 * Transport: after the lobby starts, the shim negotiates WebRTC data
 * channels with each peer via the platform's relay (signaling). Once a
 * channel is open, game data flows P2P — the server is out of the data
 * path. If WebRTC fails (NAT, firewall, old browser), the shim
 * transparently falls back to the relay for that peer.
 *
 * Connection health: a 15s keepalive ping/pong runs on each data
 * channel. If a pong isn't received within 5s, the channel is marked
 * failed and the shim falls back to relay. A reconnection attempt is
 * made after 5s — if it succeeds, the peer switches back to P2P.
 *
 * Full protocol + limits: <base>/docs and docs/DEVELOPER.md.
 */
(function () {
  'use strict';

  if (window.PlayMore) return;

  var parent = window.parent;
  var parentOrigin = null;

  var ctx = { code: '', gameId: '', you: null, host: false, players: [], metadata: null, spectator: false };
  var started = false;

  // ── WebRTC state ──────────────────────────────────────────────
  // peers[peerId] = {
  //   pc: RTCPeerConnection,
  //   dc: RTCDataChannel,       // reliable channel (ordered)
  //   dcRT: RTCDataChannel,     // unreliable channel (maxRetransmits=0)
  //   state: 'new' | 'connecting' | 'open' | 'failed',
  //   isOfferer: bool,
  //   keepaliveTimer: timer,
  //   pongTimer: timer,
  //   reconnectTimer: timer,
  //   reconnectAttempts: int,
  //   bytesSent: int,
  //   bytesReceived: int,
  //   rtt: int,
  //   lastPingTime: int
  // }
  var peers = {};
  var rtcIceServers = [{ urls: 'stun:stun.l.google.com:19302' }];
  var transportChangeHandlers = [];
  var pingChangeHandlers = [];
  var topology = 'mesh'; // 'mesh' (default) or 'star' (host-authoritative)

  // ── Config ────────────────────────────────────────────────────
  var KEEPALIVE_INTERVAL = 15000;  // 15s ping
  var PONG_TIMEOUT = 5000;         // 5s to receive pong before marking failed
  var RECONNECT_DELAY = 5000;      // 5s before reconnection attempt
  var RECONNECT_MAX_ATTEMPTS = 3;  // give up after 3 tries
  var STAGGER_DELAY = 200;         // 200ms between initiating each peer connection

  // ── Global bandwidth stats ────────────────────────────────────
  var totalBytesSent = 0;
  var totalBytesReceived = 0;

  var handlers = {
    ready: [],
    message: [],
    players: [],
    closed: [],
    lobbyState: [],
    launch: [],
    matchmaking: []
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
      try { list[i](a, b); } catch (e) {}
    }
  }

  function post(obj, origin) {
    try { parent.postMessage(obj, origin || '*'); } catch (e) {}
  }

  // cmd builds a lobby-control API method: it posts to the SPA bridge only when
  // embedded (parentOrigin resolved) and returns API for chaining. `build`
  // turns the call arguments into the message object.
  function cmd(build) {
    return function () {
      if (parentOrigin === null) return API;
      post(build.apply(null, arguments), parentOrigin);
      return API;
    };
  }

  // ── Signaling ─────────────────────────────────────────────────
  function signalSend(to, signal) {
    post({ playmore: 'send', to: to, data: { __pm_rtc: signal } }, parentOrigin);
  }

  // ── WebRTC mesh management ────────────────────────────────────

  function shouldOffer(myId, peerId) {
    return myId < peerId;
  }

  // shouldConnect decides whether to initiate a WebRTC connection to
  // peerId. In mesh topology (default), connect to everyone. In star
  // topology, only the host connects to all peers; non-host peers only
  // connect to the host.
  // Merge a server lobby snapshot into ctx (code, roster, metadata, and our
  // own host flag after a possible host migration). Shared by the lobby_state
  // and launch handlers so they never drift.
  function applyLobbyState(l) {
    if (!l) return;
    ctx.code = l.code || ctx.code;
    ctx.players = l.players || ctx.players;
    ctx.metadata = l.metadata !== undefined ? l.metadata : ctx.metadata;
    if (ctx.you) {
      for (var i = 0; i < ctx.players.length; i++) {
        if (ctx.players[i].id === ctx.you.id) { ctx.host = !!ctx.players[i].host; break; }
      }
    }
  }

  // Initiate WebRTC connections to every peer we should connect to and aren't
  // already connected to. Staggered on start to avoid a signaling burst; called
  // immediately (stagger=false) for late joins. No-op until the lobby starts.
  function initPeersNow(stagger) {
    if (!started || !ctx.you) return;
    var toConnect = [];
    for (var i = 0; i < ctx.players.length; i++) {
      var pid = ctx.players[i].id;
      if (pid && pid !== ctx.you.id && !peers[pid] && shouldConnect(pid)) toConnect.push(pid);
    }
    for (var j = 0; j < toConnect.length; j++) {
      (function (peerId, delay) {
        setTimeout(function () { initPeer(peerId); }, delay);
      })(toConnect[j], stagger ? j * STAGGER_DELAY : 0);
    }
  }

  function shouldConnect(peerId) {
    if (topology !== 'star') return true;
    if (!ctx.you) return true;
    // Host connects to everyone. Non-host only connects to the host.
    if (ctx.host) return true;
    // I'm not the host — only connect to the host.
    var hostPlayer = null;
    for (var i = 0; i < ctx.players.length; i++) {
      if (ctx.players[i].host) { hostPlayer = ctx.players[i].id; break; }
    }
    return peerId === hostPlayer;
  }

  function initPeer(peerId) {
    if (peers[peerId]) return;
    var myId = ctx.you ? ctx.you.id : '';
    var isOfferer = shouldOffer(myId, peerId);
    peers[peerId] = {
      pc: null, dc: null, dcRT: null, state: 'new', isOfferer: isOfferer,
      keepaliveTimer: null, pongTimer: null, reconnectTimer: null,
      reconnectAttempts: 0, bytesSent: 0, bytesReceived: 0,
      rtt: -1, lastPingTime: 0
    };

    createPeerConnection(peerId);
  }

  function createPeerConnection(peerId) {
    var p = peers[peerId];
    if (!p) return;

    try {
      var pc = new RTCPeerConnection({ iceServers: rtcIceServers });
      p.pc = pc;

      if (p.isOfferer) {
        var dc = pc.createDataChannel('pm', { ordered: true });
        setupDataChannel(peerId, dc, false);
        var dcRT = pc.createDataChannel('pm-rt', { ordered: false, maxRetransmits: 0 });
        setupDataChannel(peerId, dcRT, true);
      } else {
        pc.ondatachannel = function (ev) {
          var unreliable = ev.channel.label === 'pm-rt';
          setupDataChannel(peerId, ev.channel, unreliable);
        };
      }

      pc.onicecandidate = function (ev) {
        if (ev.candidate) {
          signalSend(peerId, { type: 'ice', candidate: ev.candidate });
        }
      };

      pc.onconnectionstatechange = function () {
        var st = pc.connectionState;
        if (st === 'failed' || st === 'disconnected' || st === 'closed') {
          handlePeerFailure(peerId);
        }
      };

      if (p.isOfferer) {
        pc.createOffer().then(function (offer) {
          return pc.setLocalDescription(offer);
        }).then(function () {
          signalSend(peerId, { type: 'offer', sdp: pc.localDescription });
        }).catch(function () {
          setPeerState(peerId, 'failed');
        });
      }

      setPeerState(peerId, 'connecting');
    } catch (e) {
      setPeerState(peerId, 'failed');
    }
  }

  function setupDataChannel(peerId, dc, unreliable) {
    var p = peers[peerId];
    if (!p) return;

    if (unreliable) {
      p.dcRT = dc;
    } else {
      p.dc = dc;
    }

    // Only the reliable channel triggers state transitions + keepalive.
    if (!unreliable) {
      dc.onopen = function () {
        p.reconnectAttempts = 0;
        setPeerState(peerId, 'open');
        startKeepalive(peerId);
      };
      dc.onclose = function () {
        handlePeerFailure(peerId);
      };
      dc.onerror = function () {
        handlePeerFailure(peerId);
      };
    }

    dc.onmessage = function (ev) {
      var data;
      try { data = JSON.parse(ev.data); } catch { data = ev.data; }

      // Internal keepalive — don't emit to game.
      if (data && data.__pm_ping) {
        sendRawDataChannel(peerId, { __pm_pong: true });
        return;
      }
      if (data && data.__pm_pong) {
        clearPongTimer(peerId);
        if (p.lastPingTime > 0) {
          var newRtt = Date.now() - p.lastPingTime;
          if (Math.abs(newRtt - p.rtt) >= 20) {
            p.rtt = newRtt;
            for (var qi = 0; qi < pingChangeHandlers.length; qi++) {
              try { pingChangeHandlers[qi](peerId, newRtt); } catch {}
            }
          } else {
            p.rtt = newRtt;
          }
        }
        return;
      }

      p.bytesReceived += ev.data.length || 0;
      totalBytesReceived += ev.data.length || 0;

      emit('message', peerId, data);
    };
  }

  // ── Keepalive (Phase 2) ───────────────────────────────────────
  // Every 15s, send a ping over the data channel. If no pong within
  // 5s, the channel is dead — fall back to relay and attempt reconnect.

  function startKeepalive(peerId) {
    stopKeepalive(peerId);
    var p = peers[peerId];
    if (!p) return;
    p.keepaliveTimer = setInterval(function () {
      if (p.state !== 'open') { stopKeepalive(peerId); return; }
      p.lastPingTime = Date.now();
      sendRawDataChannel(peerId, { __pm_ping: true });
      // Set pong timeout — if no pong, mark failed.
      clearPongTimer(peerId);
      p.pongTimer = setTimeout(function () {
        handlePeerFailure(peerId);
      }, PONG_TIMEOUT);
    }, KEEPALIVE_INTERVAL);
  }

  function stopKeepalive(peerId) {
    var p = peers[peerId];
    if (!p) return;
    if (p.keepaliveTimer) { clearInterval(p.keepaliveTimer); p.keepaliveTimer = null; }
    clearPongTimer(peerId);
  }

  function clearPongTimer(peerId) {
    var p = peers[peerId];
    if (!p) return;
    if (p.pongTimer) { clearTimeout(p.pongTimer); p.pongTimer = null; }
  }

  // ── Failure handling + reconnection (Phase 2) ────────────────

  function handlePeerFailure(peerId) {
    var p = peers[peerId];
    if (!p) return;

    stopKeepalive(peerId);
    setPeerState(peerId, 'failed');

    // Close the dead connection.
    try { if (p.dc) p.dc.close(); } catch {}
    try { if (p.dcRT) p.dcRT.close(); } catch {}
    try { if (p.pc) p.pc.close(); } catch {}
    p.dc = null;
    p.dcRT = null;
    p.pc = null;

    // Attempt reconnection (limited tries).
    if (p.reconnectAttempts < RECONNECT_MAX_ATTEMPTS) {
      p.reconnectAttempts++;
      p.reconnectTimer = setTimeout(function () {
        // Only reconnect if the lobby is still active.
        if (!started) return;
        p.reconnectTimer = null;
        createPeerConnection(peerId);
      }, RECONNECT_DELAY);
    }
  }

  function setPeerState(peerId, state) {
    if (!peers[peerId]) return;
    var prev = peers[peerId].state;
    peers[peerId].state = state;
    if (prev !== state) {
      for (var i = 0; i < transportChangeHandlers.length; i++) {
        try { transportChangeHandlers[i](peerId, transportFor(peerId)); } catch {}
      }
    }
  }

  function transportFor(peerId) {
    var p = peers[peerId];
    return (p && p.dc && p.state === 'open') ? 'webrtc' : 'relay';
  }

  function closePeer(peerId) {
    var p = peers[peerId];
    if (!p) return;
    stopKeepalive(peerId);
    if (p.reconnectTimer) { clearTimeout(p.reconnectTimer); p.reconnectTimer = null; }
    try { if (p.dc) p.dc.close(); } catch {}
    try { if (p.dcRT) p.dcRT.close(); } catch {}
    try { if (p.pc) p.pc.close(); } catch {}
    p.dc = null;
    p.dcRT = null;
    p.pc = null;
    p.state = 'failed';
  }

  function closeAllPeers() {
    for (var id in peers) closePeer(id);
    peers = {};
  }

  // ── Inbound signaling ─────────────────────────────────────────

  function handleSignal(from, signal) {
    if (!peers[from]) initPeer(from);
    var p = peers[from];
    if (!p || !p.pc) return;

    if (signal.type === 'offer' || signal.type === 'answer') {
      p.pc.setRemoteDescription(signal.sdp).then(function () {
        if (signal.type === 'offer' && !p.isOfferer) {
          p.pc.createAnswer().then(function (answer) {
            return p.pc.setLocalDescription(answer);
          }).then(function () {
            signalSend(from, { type: 'answer', sdp: p.pc.localDescription });
          }).catch(function () {
            setPeerState(from, 'failed');
          });
        }
      }).catch(function () {
        setPeerState(from, 'failed');
      });
    } else if (signal.type === 'ice' && signal.candidate) {
      p.pc.addIceCandidate(signal.candidate).catch(function () {});
    }
  }

  // ── Send ──────────────────────────────────────────────────────

  function sendViaRelay(to, data) {
    post({ playmore: 'send', to: to || '', data: data }, parentOrigin);
  }

  function sendRawDataChannel(peerId, data) {
    var p = peers[peerId];
    if (p && p.dc && p.state === 'open') {
      try {
        var raw = JSON.stringify(data);
        p.dc.send(raw);
        p.bytesSent += raw.length;
        totalBytesSent += raw.length;
        return true;
      } catch (e) {
        handlePeerFailure(peerId);
        return false;
      }
    }
    return false;
  }

  function sendViaDataChannel(peerId, data) {
    var p = peers[peerId];
    if (p && p.dc && p.state === 'open') {
      try {
        var raw = JSON.stringify(data);
        p.dc.send(raw);
        p.bytesSent += raw.length;
        totalBytesSent += raw.length;
        return true;
      } catch (e) {
        handlePeerFailure(peerId);
        return false;
      }
    }
    return false;
  }

  function sendViaDataChannelUnreliable(peerId, data) {
    var p = peers[peerId];
    if (p && p.dcRT && p.state === 'open') {
      try {
        var raw = JSON.stringify(data);
        p.dcRT.send(raw);
        p.bytesSent += raw.length;
        totalBytesSent += raw.length;
        return true;
      } catch (e) {
        // Unreliable channel errors are non-fatal — fall back to reliable.
        return sendViaDataChannel(peerId, data);
      }
    }
    return false;
  }

  // ── Message listener ──────────────────────────────────────────

  window.addEventListener('message', function (ev) {
    if (ev.source !== parent) return;
    var d = ev.data;
    if (!d || typeof d !== 'object' || !d.playmore) return;

    if (parentOrigin === null) parentOrigin = ev.origin;

    switch (d.playmore) {
      case 'init':
        ctx = {
          code: d.code || '',
          gameId: d.game_id || '',
          you: d.you || null,
          host: !!d.host,
          players: d.players || [],
          sessionToken: d.session_token || '',
          metadata: d.metadata || null,
          spectator: !!d.spectator
        };
        if (d.rtc_config && d.rtc_config.iceServers) {
          rtcIceServers = d.rtc_config.iceServers;
        }
        if (d.topology) {
          topology = d.topology;
        }
        // Fire onReady so the game can show its menu. When init carries a
        // populated code (the game loaded into an already-started lobby, e.g.
        // an iframe reload mid-game), the lobby is live: start immediately and
        // establish P2P. Otherwise the game is pre-lobby and should render its
        // menu and call createLobby/joinLobby/quickPlay.
        if (ctx.code) {
          started = true;
        }
        emit('ready', ctx);
        if (started) initPeersNow(true);
        break;
      case 'lobby_state':
        // Lobby state update (lobby created, player joined/left, ready toggled,
        // host migrated, metadata changed). Update ctx and connect to any newly
        // joined peers if the game is already running.
        if (d.lobby) {
          applyLobbyState(d.lobby);
          initPeersNow(false);
          emit('lobbyState', d.lobby);
          emit('players', ctx.players);
        }
        break;
      case 'launch':
        // The lobby started (host pressed start, or matchmaking filled). Mark
        // the game live, establish P2P to every peer, and fire onLaunch. onReady
        // already fired at init with the pre-lobby menu context, so it is NOT
        // fired again here — the game transitions menu -> playing via onLaunch.
        if (d.lobby) {
          applyLobbyState(d.lobby);
          started = true;
          initPeersNow(true);
          emit('launch', d.lobby);
        }
        break;
      case 'matchmaking':
        emit('matchmaking', { queueSize: d.queue_size, targetCount: d.target_count });
        break;
      case 'msg':
        if (d.data && typeof d.data === 'object' && d.data.__pm_rtc) {
          handleSignal(d.from, d.data.__pm_rtc);
        } else {
          emit('message', d.from, d.data);
        }
        break;
      case 'closed':
        started = false;
        closeAllPeers();
        emit('closed');
        break;
    }
  });

  // ── Public API ────────────────────────────────────────────────

  var API = {
    onReady: on('ready'),
    onMessage: on('message'),
    onPlayers: on('players'),
    onClosed: on('closed'),
    onLobbyState: on('lobbyState'),
    onLaunch: on('launch'),
    onMatchmaking: on('matchmaking'),

    send: function (data, to) {
      if (!started || parentOrigin === null) return API;

      if (to) {
        if (!sendViaDataChannel(to, data)) {
          sendViaRelay(to, data);
        }
      } else {
        for (var i = 0; i < ctx.players.length; i++) {
          var pid = ctx.players[i].id;
          if (pid && pid !== (ctx.you ? ctx.you.id : '')) {
            if (!sendViaDataChannel(pid, data)) {
              sendViaRelay(pid, data);
            }
          }
        }
      }
      return API;
    },

    /* Send via the unreliable data channel (maxRetransmits=0, unordered).
     * For high-frequency state updates where stale data should be dropped,
     * not retransmitted (e.g., position sync). Falls back to reliable
     * send() if the unreliable channel isn't available. */
    sendUnreliable: function (data, to) {
      if (!started || parentOrigin === null) return API;

      if (to) {
        if (!sendViaDataChannelUnreliable(to, data)) {
          if (!sendViaDataChannel(to, data)) sendViaRelay(to, data);
        }
      } else {
        for (var i = 0; i < ctx.players.length; i++) {
          var pid = ctx.players[i].id;
          if (pid && pid !== (ctx.you ? ctx.you.id : '')) {
            if (!sendViaDataChannelUnreliable(pid, data)) {
              if (!sendViaDataChannel(pid, data)) sendViaRelay(pid, data);
            }
          }
        }
      }
      return API;
    },

    players: function () { return ctx.players.slice(); },
    me: function () { return ctx.you; },
    isHost: function () { return ctx.host; },
    code: function () { return ctx.code; },
    gameId: function () { return ctx.gameId; },
    sessionToken: function () { return ctx.sessionToken; },
    metadata: function () { return ctx.metadata; },
    isActive: function () { return started; },
    isSpectator: function () { return !!ctx.spectator; },

    /* Recommended send throttle (ms) based on average peer RTT.
     * Games should wait at least this long between high-frequency
     * sends (e.g., position updates). Returns 66ms default if no
     * RTT data is available. */
    recommendedThrottle: function () {
      var sum = 0, count = 0;
      for (var id in peers) {
        if (peers[id].rtt >= 0) { sum += peers[id].rtt; count++; }
      }
      if (count === 0) return 66;
      var avg = sum / count;
      if (avg < 50) return 33;
      if (avg < 150) return 66;
      return 100;
    },

    /* Set the WebRTC topology. 'mesh' (default) = every peer connects
     * to every other peer. 'star' = all peers connect to the host only
     * (fewer connections, host is authoritative). Must be called before
     * the lobby starts (before onReady). */
    setTopology: function (t) {
      if (!started) topology = t;
      return API;
    },

    /* Update lobby metadata (host-only). The metadata object is an
     * opaque JSON value — game settings like map, difficulty, mode.
     * Non-host callers are rejected by the server. */
    setMetadata: function (obj) {
      if (parentOrigin === null) return API;
      post({ playmore: 'set_metadata', metadata: obj }, parentOrigin);
      return API;
    },

    /* ── Lobby control (game-managed lobby UI) ────────────────── */

    /* Create a new lobby for this game. The caller becomes the host.
     * Results in an onLobbyState callback with the new lobby's code. */
    createLobby: cmd(function (opts) {
      return { playmore: 'create_lobby', public: !!(opts && opts.public) };
    }),

    /* Join an existing lobby by code. Results in onLobbyState. */
    joinLobby: cmd(function (code) {
      return { playmore: 'join_lobby', code: code };
    }),

    /* Quick Play — auto-match with random players. Results in
     * onMatchmaking callbacks (queue status) and then onLaunch. */
    quickPlay: cmd(function (playerCount) {
      return { playmore: 'quick_play', player_count: playerCount || 2 };
    }),

    /* Toggle ready state (non-host). Results in onLobbyState. */
    readyUp: cmd(function (ready) {
      return { playmore: 'ready_up', ready: ready };
    }),

    /* Start the game (host-only). Results in onLaunch. */
    startGame: cmd(function () {
      return { playmore: 'start_game' };
    }),

    /* Leave the current lobby. Results in onClosed. */
    leaveLobby: cmd(function () {
      return { playmore: 'leave_lobby' };
    }),

    /* Cancel matchmaking search. */
    cancelMatchmake: cmd(function () {
      return { playmore: 'cancel_matchmake' };
    }),

    transport: function (peerId) {
      return transportFor(peerId);
    },
    onTransportChange: function (fn) {
      if (typeof fn === 'function') transportChangeHandlers.push(fn);
      return API;
    },

    /* Connection quality — RTT in ms from keepalive ping/pong.
     * Returns -1 for relay peers or unknown peers. */
    ping: function (peerId) {
      var p = peers[peerId];
      return p ? p.rtt : -1;
    },
    onPingChange: function (fn) {
      if (typeof fn === 'function') pingChangeHandlers.push(fn);
      return API;
    },

    /* Bandwidth stats (Phase 3). Returns per-peer and aggregate. */
    stats: function () {
      var peerStats = {};
      for (var id in peers) {
        peerStats[id] = {
          transport: transportFor(id),
          ping: peers[id].rtt,
          sent: peers[id].bytesSent,
          received: peers[id].bytesReceived
        };
      }
      return {
        sent: totalBytesSent,
        received: totalBytesReceived,
        peers: peerStats
      };
    }
  };

  window.PlayMore = API;

  post({ playmore: 'ready' });
})();
