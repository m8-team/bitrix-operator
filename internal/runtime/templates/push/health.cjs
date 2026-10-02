'use strict';
// Dependency readiness is separate from TCP process liveness.
const fs = require('node:fs');
const net = require('node:net');
const timeout = setTimeout(() => process.exit(1), 2000);
const done = code => {clearTimeout(timeout); process.exit(code);};
try {
  const config = JSON.parse(fs.readFileSync('/tmp/push/config.json', 'utf8'));
  const socket = net.connect(config.servers[0].port, '127.0.0.1');
  socket.on('error', () => done(1));
  socket.on('connect', () => {
    socket.destroy();
    if (process.argv[2] === 'live') return done(0);
    const redis = require('/opt/push-server/node_modules/redis');
    const client = redis.createClient({host: config.storage.host, port: config.storage.port, password: config.storage.password, retry_strategy: () => undefined});
    client.on('error', () => done(1));
    client.ping((error, reply) => done(!error && reply === 'PONG' ? 0 : 1));
  });
} catch (_) {done(1);}
