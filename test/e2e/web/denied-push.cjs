'use strict';
const dns = require('node:dns');
const net = require('node:net');
// DNS must resolve; a lookup failure is not evidence of policy enforcement.
dns.lookup(process.argv[2], {family: 4}, (error, address) => {
  if (error) {console.error('Policy test DNS lookup failed'); process.exit(1);}
  const socket = net.connect(Number(process.argv[3]), address);
  const finish = code => {socket.destroy(); process.exit(code);};
  socket.setTimeout(3000, () => finish(0));
  socket.on('connect', () => {console.error('Forbidden Push connection was allowed'); finish(1);});
  socket.on('error', failure => finish(['ECONNREFUSED', 'ETIMEDOUT', 'EHOSTUNREACH'].includes(failure.code) ? 0 : 1));
});
