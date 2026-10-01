'use strict';
// Protocol test against the real upstream server, not a synthetic Push implementation.
const http = require('node:http');
const crypto = require('node:crypto');
const fs = require('node:fs');
const WS = require('/opt/push-server/node_modules/ws');
const models = require('/opt/push-server/lib/models');
const config = JSON.parse(fs.readFileSync('/tmp/push/config.json', 'utf8'));
const key = config.security.key;
const pub = process.argv[2] || 'http://publisher:9010';
const subs = (process.argv[3] || 'http://subscriber:8010,http://subscriber2:8010').split(',');
const rest = process.argv[4] || pub;
const pollingPath = process.argv[5] || '/bitrix/subws/';
const sockets = [];
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
    r.setTimeout(8000, () => r.destroy(new Error('HTTP timeout')));
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
    const timer = setTimeout(() => reject(new Error('Event not received')), 8000);
    ws.on('message', data => {if (data.toString().includes(marker)) {clearTimeout(timer); resolve();}});
  });
}
(async () => {
  const privateID = channel(), publicID = channel();
  const token = privateID + ':' + publicID + '.' + sign(privateID + ':' + publicID).toString('hex');
  const peers = await Promise.all(subs.map(url => connect(url, token)));
  const marker = 'fanout-' + channel();
  const deliveries = peers.map(ws => message(ws, marker));
  assert((await request(pub + '/bitrix/pub/?CHANNEL_ID=' + privateID, 'POST', marker)).status === 200, 'Internal publish failed');
  await Promise.all(deliveries);
  peers[0].terminate();
  const reconnected = await connect(subs[0], token);
  const next = 'reconnect-' + channel();
  const delivered = message(reconnected, next);
  await request(pub + '/bitrix/pub/?CHANNEL_ID=' + privateID, 'POST', next);
  await delivered;
  const pollingID = channel();
  const polling = request(subs[0] + pollingPath + '?CHANNEL_ID=' + pollingID + '.' + sign(pollingID).toString('hex'));
  // Establish the pending HTTP subscription before publishing its independent channel.
  await new Promise(resolve => setTimeout(resolve, 150));
  const pollMarker = 'poll-' + channel();
  await request(pub + '/bitrix/pub/?CHANNEL_ID=' + pollingID, 'POST', pollMarker);
  const received = await polling;
  assert(received.status === 200 && received.body.includes(pollMarker), 'Long polling failed');
  assert((await request(subs[0] + '/bitrix/subws/?CHANNEL_ID=' + channel() + '.' + '0'.repeat(64))).status === 400, 'Invalid subscription signature accepted');
  const restMarker = 'rest-' + channel();
  const payload = signature => models.RequestBatch.encode({requests: [{incomingMessages: {messages: [{receivers: [{id: Buffer.from(publicID, 'hex'), isPrivate: false, signature}], body: restMarker}]}}]}).finish();
  const restURL = rest + '/bitrix/rest/?CHANNEL_ID=' + token;
  assert((await request(restURL, 'POST', payload(Buffer.alloc(32)))).status === 400, 'Invalid REST receiver signature accepted');
  const restEvent = message(reconnected, restMarker);
  assert((await request(restURL, 'POST', payload(sign('public:' + publicID)))).status === 200, 'Signed REST publish failed');
  await restEvent;
  console.log('Push protocol OK: fanout, reconnect, polling, HMAC and signed REST');
})().catch(error => {console.error(error.message); process.exitCode = 1;}).finally(() => {for (const ws of sockets) ws.terminate();});
