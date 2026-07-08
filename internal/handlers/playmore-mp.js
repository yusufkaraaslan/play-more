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

  var ctx = { code: '', gameId: '', you: null, host: false, players: [] };
  var started = false;

  // ── WebRTC state ──────────────────────────────────────────────
  // peers[peerId] = {
  //   pc: RTCPeerConnection,
  //   dc: RTCDataChannel,
  //   state: 'new' | 'connecting' | 'open' | 'failed',
  //   isOfferer: bool,
  //   keepaliveTimer: timer,
  //   pongTimer: timer,
  //   reconnectTimer: timer,
  //   reconnectAttempts: int,
  //   bytesSent: int,
  //   bytesReceived: int
  // }
  var peers = {};
  var rtcIceServers = [{ urls: 'stun:stun.l.google.com:19302' }];
  var transportChangeHandlers = [];

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
    closed: []
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

  // ── Signaling ─────────────────────────────────────────────────
  function signalSend(to, signal) {
    post({ playmore: 'send', to: to, data: { __pm_rtc: signal } }, parentOrigin);
  }

  // ── WebRTC mesh management ────────────────────────────────────

  function shouldOffer(myId, peerId) {
    return myId < peerId;
  }

  function initPeer(peerId) {
    if (peers[peerId]) return;
    var myId = ctx.you ? ctx.you.id : '';
    var isOfferer = shouldOffer(myId, peerId);
    peers[peerId] = {
      pc: null, dc: null, state: 'new', isOfferer: isOfferer,
      keepaliveTimer: null, pongTimer: null, reconnectTimer: null,
      reconnectAttempts: 0, bytesSent: 0, bytesReceived: 0
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
        setupDataChannel(peerId, dc);
      } else {
        pc.ondatachannel = function (ev) {
          setupDataChannel(peerId, ev.channel);
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

  function setupDataChannel(peerId, dc) {
    var p = peers[peerId];
    if (!p) return;
    p.dc = dc;

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
        return;
      }

      // Track bandwidth
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
    try { if (p.pc) p.pc.close(); } catch {}
    p.dc = null;
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
    try { if (p.pc) p.pc.close(); } catch {}
    p.dc = null;
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
          sessionToken: d.session_token || ''
        };
        if (d.rtc_config && d.rtc_config.iceServers) {
          rtcIceServers = d.rtc_config.iceServers;
        }
        started = true;
        emit('ready', ctx);

        // Staggered connection initiation (Phase 3) — avoids signaling
        // burst when 8 players all try to connect simultaneously.
        if (ctx.you) {
          var toConnect = [];
          for (var i = 0; i < ctx.players.length; i++) {
            var pid = ctx.players[i].id;
            if (pid && pid !== ctx.you.id) toConnect.push(pid);
          }
          for (var j = 0; j < toConnect.length; j++) {
            (function(peerId, delay) {
              setTimeout(function() { initPeer(peerId); }, delay);
            })(toConnect[j], j * STAGGER_DELAY);
          }
        }
        break;
      case 'players':
        ctx.players = d.players || [];
        emit('players', ctx.players);
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

    players: function () { return ctx.players.slice(); },
    me: function () { return ctx.you; },
    isHost: function () { return ctx.host; },
    code: function () { return ctx.code; },
    gameId: function () { return ctx.gameId; },
    sessionToken: function () { return ctx.sessionToken; },
    isActive: function () { return started; },

    transport: function (peerId) {
      return transportFor(peerId);
    },
    onTransportChange: function (fn) {
      if (typeof fn === 'function') transportChangeHandlers.push(fn);
      return API;
    },

    /* Bandwidth stats (Phase 3). Returns per-peer and aggregate. */
    stats: function () {
      var peerStats = {};
      for (var id in peers) {
        peerStats[id] = {
          transport: transportFor(id),
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
