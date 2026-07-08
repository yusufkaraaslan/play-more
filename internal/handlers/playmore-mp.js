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
 *   </script>
 *
 * Transport: after the lobby starts, the shim negotiates WebRTC data
 * channels with each peer via the platform's relay (signaling). Once a
 * channel is open, game data flows P2P — the server is out of the data
 * path. If WebRTC fails (NAT, firewall, old browser), the shim
 * transparently falls back to the relay for that peer.
 *
 * Full protocol + limits: <base>/docs and docs/DEVELOPER.md.
 */
(function () {
  'use strict';

  if (window.PlayMore) return; // idempotent if included twice

  var parent = window.parent;
  var parentOrigin = null;

  var ctx = { code: '', gameId: '', you: null, host: false, players: [] };
  var started = false;

  // ── WebRTC state ──────────────────────────────────────────────
  // peers[peerId] = {
  //   pc: RTCPeerConnection,
  //   dc: RTCDataChannel,       // set when data channel is open
  //   state: 'new' | 'connecting' | 'open' | 'failed',
  //   isOfferer: bool           // true = we create the offer
  // }
  var peers = {};
  var rtcIceServers = [{ urls: 'stun:stun.l.google.com:19302' }];
  var transportChangeHandlers = [];

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
      try { list[i](a, b); } catch (e) { /* keep going */ }
    }
  }

  function post(obj, origin) {
    try { parent.postMessage(obj, origin || '*'); } catch (e) { /* parent gone */ }
  }

  // ── Signaling: send via the existing relay path ───────────────
  // Signaling messages are wrapped in { __pm_rtc: ... } so the game's
  // onMessage callback never sees them.
  function signalSend(to, signal) {
    post({ playmore: 'send', to: to, data: { __pm_rtc: signal } }, parentOrigin);
  }

  // ── WebRTC mesh management ────────────────────────────────────

  function shouldOffer(myId, peerId) {
    // Deterministic: lexicographically smaller ID creates the offer.
    // This avoids "glare" (both sides offering simultaneously).
    return myId < peerId;
  }

  function initPeer(peerId) {
    if (peers[peerId]) return;
    var myId = ctx.you ? ctx.you.id : '';
    var isOfferer = shouldOffer(myId, peerId);
    peers[peerId] = { pc: null, dc: null, state: 'new', isOfferer: isOfferer };

    try {
      var pc = new RTCPeerConnection({ iceServers: rtcIceServers });
      peers[peerId].pc = pc;

      // The offerer creates the data channel; the answerer receives it.
      if (isOfferer) {
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
          setPeerState(peerId, 'failed');
          // Close and clean up — relay fallback kicks in automatically.
          closePeer(peerId);
        }
      };

      // If offerer, create and send the offer.
      if (isOfferer) {
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
      // RTCPeerConnection not available (old browser, disabled).
      // Relay fallback handles all traffic — no action needed.
      setPeerState(peerId, 'failed');
    }
  }

  function setupDataChannel(peerId, dc) {
    peers[peerId].dc = dc;

    dc.onopen = function () {
      setPeerState(peerId, 'open');
    };

    dc.onclose = function () {
      setPeerState(peerId, 'failed');
    };

    dc.onerror = function () {
      setPeerState(peerId, 'failed');
    };

    dc.onmessage = function (ev) {
      // Data channel messages arrive as strings (we send JSON strings).
      // Emit to the game's onMessage — same as relay messages.
      var data;
      try { data = JSON.parse(ev.data); } catch { data = ev.data; }
      emit('message', peerId, data);
    };
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

  // ── Inbound signaling handler ─────────────────────────────────
  // Called from the message listener when a __pm_rtc wrapper is detected.
  function handleSignal(from, signal) {
    // Ensure peer exists. If we didn't init them yet (they're the offerer
    // and we're the answerer), create the peer now.
    if (!peers[from]) initPeer(from);
    var p = peers[from];
    if (!p || !p.pc) return;

    if (signal.type === 'offer' || signal.type === 'answer') {
      p.pc.setRemoteDescription(signal.sdp).then(function () {
        if (signal.type === 'offer' && !p.isOfferer) {
          // We're the answerer — create and send the answer.
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

  // ── Send: prefer WebRTC, fall back to relay ───────────────────

  function sendViaRelay(to, data) {
    post({ playmore: 'send', to: to || '', data: data }, parentOrigin);
  }

  function sendViaDataChannel(peerId, data) {
    var p = peers[peerId];
    if (p && p.dc && p.state === 'open') {
      try {
        p.dc.send(JSON.stringify(data));
        return true;
      } catch (e) {
        // Channel might have closed between the check and the send.
        setPeerState(peerId, 'failed');
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
        // Use ICE servers from the platform if provided.
        if (d.rtc_config && d.rtc_config.iceServers) {
          rtcIceServers = d.rtc_config.iceServers;
        }
        started = true;
        emit('ready', ctx);

        // Initiate WebRTC mesh with all peers (except self).
        if (ctx.you) {
          for (var i = 0; i < ctx.players.length; i++) {
            var pid = ctx.players[i].id;
            if (pid && pid !== ctx.you.id) {
              initPeer(pid);
            }
          }
        }
        break;
      case 'players':
        ctx.players = d.players || [];
        emit('players', ctx.players);
        break;
      case 'msg':
        // Check for internal signaling messages — don't emit to game.
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

    /* Relay `data` to the lobby. Omit `to` to broadcast; pass a
     * player id to unicast. Uses WebRTC data channels when available,
     * falls back to the server relay transparently. */
    send: function (data, to) {
      if (!started || parentOrigin === null) return API;

      if (to) {
        // Unicast: try data channel first, relay fallback.
        if (!sendViaDataChannel(to, data)) {
          sendViaRelay(to, data);
        }
      } else {
        // Broadcast: for each peer, use data channel or relay.
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

    /* Lobby snapshot accessors. */
    players: function () { return ctx.players.slice(); },
    me: function () { return ctx.you; },
    isHost: function () { return ctx.host; },
    code: function () { return ctx.code; },
    gameId: function () { return ctx.gameId; },
    sessionToken: function () { return ctx.sessionToken; },
    isActive: function () { return started; },

    /* Transport reporting. */
    transport: function (peerId) {
      return transportFor(peerId);
    },
    onTransportChange: function (fn) {
      if (typeof fn === 'function') transportChangeHandlers.push(fn);
      return API;
    }
  };

  window.PlayMore = API;

  // Announce readiness.
  post({ playmore: 'ready' });
})();
