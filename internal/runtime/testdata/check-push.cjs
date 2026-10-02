'use strict';
// Protocol test against the real upstream server, not a synthetic Push implementation.
const http = require('node:http');
const crypto = require('node:crypto');
const fs = require('node:fs');
const WS = require('/opt/push-server/node_modules/ws');
const models = require('/opt/push-server/lib/models');
const config = JSON.parse(fs.readFileSync('/tmp/push/config.json', 'utf8'));
const manifest = JSON.parse(fs.readFileSync('/run/push-config/manifest.json', 'utf8'));
// JSON conversion must preserve exact UTF-8 bytes, including BOM, quotes and whitespace.
if (!Buffer.from(config.security.key).equals(fs.readFileSync(manifest.keyFile)) ||
    !Buffer.from(config.storage.password).equals(fs.readFileSync(manifest.redis.passwordFile))) {
  throw new Error('Secret bytes changed during JSON serialization');
}
const key = config.security.key;
const pub = process.argv[2] || 'http://publisher:9010';
const subs = (process.argv[3] || 'http://subscriber:8010,http://subscriber2:8010').split(',');
const rest = process.argv[4] || pub;
const pollingPath = process.argv[5] || '/bitrix/subws/';
const sockets = [];
const requests = new Set();
const timers = new Set();
const sign = data => crypto.createHmac('sha256', key).update(data).digest();
const channel = () => crypto.randomBytes(16).toString('hex');
const assert = (condition, message) => {if (!condition) throw new Error(message);};
function request(url, method = 'GET', body = '') {
  return new Promise((resolve, reject) => {
    const r = http.request(url, {method, headers: {'Content-Type': 'application/octet-stream', 'Host': 'portal.example.com'}}, response => {
      const chunks = [];
      response.on('data', data => chunks.push(data));
      response.on('end', () => resolve({status: response.statusCode, body: Buffer.concat(chunks).toString()}));
    });
    requests.add(r);
    r.on('close', () => requests.delete(r));
    r.setTimeout(8000, () => r.destroy(new Error('HTTP timeout: ' + new URL(url).pathname)));
    r.on('error', reject); r.end(body);
  });
}
function connect(url, token) {
  return new Promise((resolve, reject) => {
    const ws = new WS(url.replace('http:', 'ws:') + '/bitrix/subws/?CHANNEL_ID=' + token, {headers: {'Host': 'portal.example.com'}, handshakeTimeout: 5000});
    sockets.push(ws);
    ws.on('error', reject);
    ws.on('open', () => resolve(ws));
  });
}
function message(ws, marker) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('Event not received: ' + marker.split('-')[0])), 8000);
    timers.add(timer);
    ws.on('message', data => {if (data.toString().includes(marker)) {clearTimeout(timer); resolve();}});
  });
}
(async () => {
  console.log('Push check: WebSocket fanout');
  const privateID = channel(), publicID = channel();
  const token = privateID + ':' + publicID + '.' + sign(privateID + ':' + publicID).toString('hex');
  const peers = await Promise.all(subs.map(url => connect(url, token)));
  const marker = 'fanout-' + channel();
  const deliveries = peers.map(ws => message(ws, marker));
  const [published] = await Promise.all([request(pub + '/bitrix/pub/?CHANNEL_ID=' + privateID, 'POST', marker), ...deliveries]);
  assert(published.status === 200, 'Internal publish failed');
  console.log('Push check: reconnect');
  peers[0].terminate();
  const reconnected = await connect(subs[0], token);
  const next = 'reconnect-' + channel();
  const delivered = message(reconnected, next);
  await Promise.all([request(pub + '/bitrix/pub/?CHANNEL_ID=' + privateID, 'POST', next), delivered]);
  console.log('Push check: long polling');
  const pollingID = channel();
  const polling = request(subs[0] + pollingPath + '?CHANNEL_ID=' + pollingID + '.' + sign(pollingID).toString('hex'));
  const pollMarker = 'poll-' + channel();
  const [received] = await Promise.all([polling, (async () => {
    // Establish the pending HTTP subscription before publishing its independent channel.
    await new Promise(resolve => setTimeout(resolve, 150));
    return request(pub + '/bitrix/pub/?CHANNEL_ID=' + pollingID, 'POST', pollMarker);
  })()]);
  assert(received.status === 200 && received.body.includes(pollMarker), 'Long polling failed');
  console.log('Push check: invalid subscription signature');
  assert((await request(subs[0] + '/bitrix/subws/?CHANNEL_ID=' + channel() + '.' + '0'.repeat(64))).status === 400, 'Invalid subscription signature accepted');
  const restMarker = 'rest-' + channel();
  const payload = signature => models.RequestBatch.encode({requests: [{incomingMessages: {messages: [{receivers: [{id: Buffer.from(publicID, 'hex'), isPrivate: false, signature}], body: restMarker}]}}]}).finish();
  const restURL = rest + '/bitrix/rest/?CHANNEL_ID=' + token;
  console.log('Push check: invalid REST signature');
  assert((await request(restURL, 'POST', payload(Buffer.alloc(32)))).status === 400, 'Invalid REST receiver signature accepted');
  console.log('Push check: signed REST');
  const restEvent = message(reconnected, restMarker);
  const [restResult] = await Promise.all([request(restURL, 'POST', payload(sign('public:' + publicID))), restEvent]);
  assert(restResult.status === 200, 'Signed REST publish failed');
  console.log('Push protocol OK: fanout, reconnect, polling, HMAC and signed REST');
})().catch(error => {console.error(error.message); process.exitCode = 1;}).finally(() => {
  for (const timer of timers) clearTimeout(timer);
  for (const r of requests) r.destroy();
  for (const ws of sockets) ws.terminate();
});
