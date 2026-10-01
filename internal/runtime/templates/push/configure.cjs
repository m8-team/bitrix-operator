'use strict';
// Parse native JSON, insert Secret values with JSON serialization, never envsubst.
const fs = require('node:fs');
try {
  const role = process.argv[2];
  if (!['publisher', 'subscriber'].includes(role)) throw new Error();
  const manifest = JSON.parse(fs.readFileSync(__dirname + '/manifest.json', 'utf8'));
  const readSecret = path => {
    if (!/^\/run\/bitrix-secrets\/[a-f0-9]{64}$/.test(path)) throw new Error();
    const value = fs.readFileSync(path);
    if (!value.length || value.length > 1048576) throw new Error();
    return new TextDecoder('utf-8', {fatal: true}).decode(value);
  };
  const config = JSON.parse(fs.readFileSync(__dirname + '/' + role + '.json', 'utf8'));
  config.security.key = readSecret(manifest.keyFile);
  if (Buffer.byteLength(config.security.key) < 32 || Buffer.byteLength(config.security.key) > 512) throw new Error();
  config.storage.host = manifest.redis.host;
  config.storage.port = manifest.redis.port;
  config.storage.password = readSecret(manifest.redis.passwordFile);
  config.processUniqueId = require('node:os').hostname() + '-' + process.pid;
  for (const dir of ['/tmp/push', config.dataDir, config.debug.folderName]) fs.mkdirSync(dir, {recursive: true, mode: 0o700});
  fs.writeFileSync('/tmp/push/config.json', JSON.stringify(config), {mode: 0o600});
} catch (_) {
  console.error('Push configuration unavailable');
  process.exit(1);
}
