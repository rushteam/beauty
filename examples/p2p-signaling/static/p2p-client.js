/**
 * P2PClient — beauty/pkg/p2p/signaling 的浏览器客户端库。
 *
 * 功能:
 *   - 信令连接 + WebRTC DataChannel 建连
 *   - 多通道支持(可靠/不可靠/有限重传)
 *   - KeepAlive 心跳(防反代关闭空闲连接)
 *   - 自动重连(可配重试次数)
 *
 * 用法:
 *   const p2p = new P2PClient('ws://localhost:8080/ws', {
 *     room: 'game-1',
 *     peerId: 'alice',
 *     iceServers: [{urls:'stun:stun.l.google.com:19302'}],
 *     keepAliveInterval: 10000,   // 心跳间隔(ms),默认 10s
 *     reconnectAttempts: 3,       // 信令断线重连次数,默认 3
 *     onPeerConnected(peerId) { ... },
 *     onPeerDisconnected(peerId) { ... },
 *     onMessage(peerId, channel, data) { ... },
 *   });
 *   await p2p.join();
 *   p2p.send('reliable', 'hello');
 *   p2p.sendTo('bob', 'unreliable', positionBytes);
 *   p2p.leave();
 */
class P2PClient {
  /**
   * @param {string} signalingUrl - 信令服务 WebSocket 地址
   * @param {object} options
   */
  constructor(signalingUrl, options = {}) {
    this.url = signalingUrl;
    this.room = options.room || 'default';
    this.requestedId = options.peerId || '';
    this.iceServers = options.iceServers || [{ urls: 'stun:stun.l.google.com:19302' }];
    this.keepAliveInterval = options.keepAliveInterval || 10000;
    this.reconnectAttempts = options.reconnectAttempts ?? 3;

    this.onPeerConnected = options.onPeerConnected || (() => {});
    this.onPeerDisconnected = options.onPeerDisconnected || (() => {});
    this.onMessage = options.onMessage || (() => {});
    this.onStateChange = options.onStateChange || (() => {});

    // 兼容旧 API
    this.onReliableMessage = options.onReliableMessage || null;
    this.onUnreliableMessage = options.onUnreliableMessage || null;

    this.localId = '';
    this.peers = new Map();
    this.channelConfigs = [];
    this.ws = null;
    this._keepAliveTimer = null;
    this._reconnectCount = 0;
    this._intentionalClose = false;
  }

  /**
   * 加入房间。返回 Promise,resolve 时已获得 peer ID。
   * @returns {Promise<string>}
   */
  join() {
    this._intentionalClose = false;
    return this._connect();
  }

  _connect() {
    return new Promise((resolve, reject) => {
      this.onStateChange('connecting');
      this.ws = new WebSocket(this.url);

      this.ws.onopen = () => {
        this._reconnectCount = 0;
        this._send('join', { room: this.room, peer_id: this.requestedId });
        this._startKeepAlive();
      };

      this.ws.onmessage = (ev) => {
        const env = JSON.parse(ev.data);
        switch (env.event) {
          case 'assign_id': {
            const payload = env.data;
            this.localId = payload.peer_id;
            if (payload.channels && payload.channels.length > 0) {
              this.channelConfigs = payload.channels;
            }
            this.onStateChange('connected');
            resolve(payload.peer_id);
            break;
          }
          case 'peer_joined': {
            const { peer_id, initiator } = env.data;
            this._createPeer(peer_id, initiator);
            break;
          }
          case 'peer_left': {
            const { peer_id } = env.data;
            this._removePeer(peer_id);
            break;
          }
          case 'signal': {
            const { from, data } = env.data;
            this._handleSignal(from, data);
            break;
          }
          case 'keep_alive':
            break; // 心跳响应,忽略
        }
      };

      this.ws.onerror = (err) => reject(err);
      this.ws.onclose = () => {
        this._stopKeepAlive();
        if (!this._intentionalClose && this._reconnectCount < this.reconnectAttempts) {
          this._reconnectCount++;
          this.onStateChange('reconnecting');
          const delay = Math.min(1000 * Math.pow(2, this._reconnectCount - 1), 10000);
          setTimeout(() => {
            this._connect().then(resolve).catch(reject);
          }, delay);
        } else {
          this.onStateChange('disconnected');
          this.peers.forEach((_, id) => this._removePeer(id));
        }
      };
    });
  }

  /** 离开房间,关闭所有连接。 */
  leave() {
    this._intentionalClose = true;
    this._stopKeepAlive();
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this._send('leave', null);
      this.ws.close();
    }
    this.peers.forEach((peer) => peer.pc.close());
    this.peers.clear();
  }

  /**
   * 通过指定通道广播数据。
   * @param {string} channelLabel - 通道标签名(如 'reliable', 'unreliable')
   * @param {string|ArrayBuffer} data
   */
  send(channelLabel, data) {
    for (const [, peer] of this.peers) {
      const ch = peer.channels.get(channelLabel);
      if (ch && ch.readyState === 'open') ch.send(data);
    }
  }

  /**
   * 通过指定通道发送给特定 peer。
   * @param {string} peerId
   * @param {string} channelLabel
   * @param {string|ArrayBuffer} data
   */
  sendTo(peerId, channelLabel, data) {
    const peer = this.peers.get(peerId);
    if (!peer) return;
    const ch = peer.channels.get(channelLabel);
    if (ch && ch.readyState === 'open') ch.send(data);
  }

  // 兼容旧 API
  sendReliable(data) { this.send('reliable', data); }
  sendReliableTo(peerId, data) { this.sendTo(peerId, 'reliable', data); }
  sendUnreliable(data) { this.send('unreliable', data); }
  sendUnreliableTo(peerId, data) { this.sendTo(peerId, 'unreliable', data); }

  /** 返回当前已连接的 peer ID 列表。 */
  connectedPeers() {
    const ids = [];
    for (const [id, peer] of this.peers) {
      if (peer.pc.connectionState === 'connected') ids.push(id);
    }
    return ids;
  }

  /** 返回服务端下发的通道配置。 */
  getChannelConfigs() {
    return this.channelConfigs;
  }

  // ─── 内部方法 ───

  _send(event, data) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ event, data: data !== undefined ? data : undefined }));
    }
  }

  _relay(to, data) {
    this._send('relay', { to, data });
  }

  _startKeepAlive() {
    this._stopKeepAlive();
    this._keepAliveTimer = setInterval(() => {
      this._send('keep_alive', null);
    }, this.keepAliveInterval);
  }

  _stopKeepAlive() {
    if (this._keepAliveTimer) {
      clearInterval(this._keepAliveTimer);
      this._keepAliveTimer = null;
    }
  }

  _getChannelOptions(config) {
    const opts = { ordered: config.ordered };
    if (config.max_retransmits !== undefined && config.max_retransmits !== null) {
      opts.maxRetransmits = config.max_retransmits;
    }
    return opts;
  }

  _createPeer(peerId, initiator) {
    const pc = new RTCPeerConnection({ iceServers: this.iceServers });
    const entry = { pc, channels: new Map() };
    this.peers.set(peerId, entry);

    // 使用服务端下发的通道配置(或默认 reliable + unreliable)
    const configs = this.channelConfigs.length > 0
      ? this.channelConfigs
      : [
          { label: 'reliable', ordered: true, max_retransmits: null },
          { label: 'unreliable', ordered: false, max_retransmits: 0 },
        ];

    if (initiator) {
      for (const cfg of configs) {
        const ch = pc.createDataChannel(cfg.label, this._getChannelOptions(cfg));
        entry.channels.set(cfg.label, ch);
        this._setupChannel(ch, peerId);
      }
    } else {
      pc.ondatachannel = (e) => {
        const ch = e.channel;
        entry.channels.set(ch.label, ch);
        this._setupChannel(ch, peerId);
      };
    }

    pc.onicecandidate = (e) => {
      if (e.candidate) {
        this._relay(peerId, { type: 'candidate', candidate: e.candidate });
      }
    };

    pc.onconnectionstatechange = () => {
      if (pc.connectionState === 'connected') {
        this.onPeerConnected(peerId, pc);
      } else if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
        this._removePeer(peerId);
      }
    };

    if (initiator) {
      pc.createOffer()
        .then((o) => pc.setLocalDescription(o))
        .then(() => {
          this._relay(peerId, { type: 'offer', sdp: pc.localDescription.sdp });
        });
    }
  }

  _handleSignal(from, data) {
    if (!this.peers.has(from)) {
      this._createPeer(from, false);
    }
    const { pc } = this.peers.get(from);

    if (data.type === 'offer') {
      pc.setRemoteDescription({ type: 'offer', sdp: data.sdp })
        .then(() => pc.createAnswer())
        .then((a) => pc.setLocalDescription(a))
        .then(() => {
          this._relay(from, { type: 'answer', sdp: pc.localDescription.sdp });
        });
    } else if (data.type === 'answer') {
      pc.setRemoteDescription({ type: 'answer', sdp: data.sdp });
    } else if (data.type === 'candidate') {
      pc.addIceCandidate(data.candidate).catch(() => {});
    }
  }

  _setupChannel(channel, peerId) {
    channel.onmessage = (e) => {
      // 通用回调
      this.onMessage(peerId, channel.label, e.data);
      // 兼容旧 API
      if (channel.label === 'reliable' && this.onReliableMessage) {
        this.onReliableMessage(peerId, e.data);
      } else if (channel.label === 'unreliable' && this.onUnreliableMessage) {
        this.onUnreliableMessage(peerId, e.data);
      }
    };
    channel.onclose = () => {};
  }

  _removePeer(peerId) {
    const peer = this.peers.get(peerId);
    if (peer) {
      peer.pc.close();
      this.peers.delete(peerId);
      this.onPeerDisconnected(peerId);
    }
  }
}

// ESM / CommonJS / 全局兼容
if (typeof module !== 'undefined' && module.exports) {
  module.exports = P2PClient;
} else if (typeof window !== 'undefined') {
  window.P2PClient = P2PClient;
}
