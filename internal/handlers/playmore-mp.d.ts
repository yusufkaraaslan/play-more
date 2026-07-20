// Type definitions for playmore-mp.js — PlayMore multiplayer lobby client
// Project: https://github.com/yusufkaraaslan/play-more
// Served at: /playmore-mp.d.ts
//
// Usage (bundler):  npm install playmore-mp
//   import PlayMore from 'playmore-mp';
//   PlayMore.onReady(ctx => { ... });
//
// Usage (script tag): add this file to your tsconfig "types" or reference it:
//   /// <reference path="/playmore-mp.d.ts" />

/** A player in the lobby roster. */
export interface Player {
  id: string;
  username: string;
  avatar_url: string;
  ready: boolean;
  host: boolean;
  spectator?: boolean;
}

/** Full lobby state snapshot, sent on every membership or state change. */
export interface LobbyState {
  code: string;
  game_id: string;
  host_id: string;
  started: boolean;
  max_players: number;
  players: Player[];
  metadata?: unknown;
}

/** Matchmaking queue status update. */
export interface MatchmakingInfo {
  queueSize: number;
  targetCount: number;
}

/** Transport type for a peer connection. */
export type Transport = 'webrtc' | 'relay';

/** Per-peer bandwidth + connection stats. */
export interface PeerStats {
  transport: Transport;
  ping: number;
  sent: number;
  received: number;
}

/** Aggregate bandwidth stats returned by `stats()`. */
export interface BandwidthStats {
  sent: number;
  received: number;
  peers: Record<string, PeerStats>;
}

/** Context object passed to `onReady`. */
export interface SessionContext {
  code: string;
  gameId: string;
  you: { id: string; username: string } | null;
  host: boolean;
  players: Player[];
  sessionToken: string;
  metadata: unknown;
  spectator: boolean;
}

/** Options for `createLobby`. */
export interface CreateLobbyOptions {
  public?: boolean;
  maxPlayers?: number;
}

/** WebRTC topology. 'mesh' = all peers connect to all. 'star' = host-authoritative. */
export type Topology = 'mesh' | 'star';

/** ICE server configuration for WebRTC. */
export interface RTCIceServer {
  urls: string | string[];
  username?: string;
  credential?: string;
}

/** Callback type signatures. */
export type ReadyCallback = (ctx: SessionContext) => void;
export type MessageCallback = (from: string, data: unknown) => void;
export type PlayersCallback = (players: Player[]) => void;
export type ClosedCallback = () => void;
export type LobbyStateCallback = (lobby: LobbyState) => void;
export type LaunchCallback = (lobby: LobbyState) => void;
export type MatchmakingCallback = (info: MatchmakingInfo) => void;
export type TransportChangeCallback = (peerId: string, transport: Transport) => void;
export type PingChangeCallback = (peerId: string, rtt: number) => void;

/**
 * The PlayMore multiplayer SDK API. All methods return `this` for chaining.
 * Callbacks fire asynchronously; lobby-entry commands (`createLobby`,
 * `joinLobby`, `quickPlay`) are queued if the socket is still connecting.
 */
export interface PlayMoreAPI {
  // ── Callbacks ──────────────────────────────────────────────
  onReady(fn: ReadyCallback): this;
  onMessage(fn: MessageCallback): this;
  onPlayers(fn: PlayersCallback): this;
  onClosed(fn: ClosedCallback): this;
  onLobbyState(fn: LobbyStateCallback): this;
  onLaunch(fn: LaunchCallback): this;
  onMatchmaking(fn: MatchmakingCallback): this;

  // ── Send game data ────────────────────────────────────────
  /** Broadcast data to all peers, or send to a specific peer. */
  send(data: unknown, to?: string): this;
  /** Send via the unreliable data channel (maxRetransmits=0). Falls back to reliable. */
  sendUnreliable(data: unknown, to?: string): this;

  // ── Lobby control (game-managed lobby UI) ─────────────────
  createLobby(opts?: CreateLobbyOptions): this;
  joinLobby(code: string): this;
  quickPlay(playerCount?: number): this;
  readyUp(ready: boolean): this;
  startGame(): this;
  leaveLobby(): this;
  cancelMatchmake(): this;

  // ── Lobby metadata ────────────────────────────────────────
  setMetadata(obj: unknown): this;

  // ── State accessors ───────────────────────────────────────
  players(): Player[];
  me(): { id: string; username: string } | null;
  isHost(): boolean;
  code(): string;
  gameId(): string;
  sessionToken(): string;
  metadata(): unknown;
  isActive(): boolean;
  isSpectator(): boolean;

  // ── Transport ──────────────────────────────────────────────
  transport(peerId: string): Transport;
  onTransportChange(fn: TransportChangeCallback): this;
  ping(peerId: string): number;
  onPingChange(fn: PingChangeCallback): this;

  // ── Bandwidth stats ───────────────────────────────────────
  stats(): BandwidthStats;

  // ── Configuration ─────────────────────────────────────────
  /** Recommended minimum interval (ms) between high-frequency sends, based on avg RTT. */
  recommendedThrottle(): number;
  /** Set the WebRTC topology. Must be called before the lobby starts. */
  setTopology(t: Topology): this;
}

/** Global PlayMore singleton, available at `window.PlayMore`. */
export const PlayMore: PlayMoreAPI;
export default PlayMore;
