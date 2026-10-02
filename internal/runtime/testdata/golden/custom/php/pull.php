<?php
return static function (array $push, callable $readSecret): array {
    if (!$push['enabled']) {
        // Do not preserve a stale release's external Push server after disabling the CR field.
        return ['nginx' => 'N', 'push' => 'N', 'websocket' => 'N', 'guest' => 'N'];
    }
    return [
        'path_to_listener' => 'http://#DOMAIN#/bitrix/sub/',
        'path_to_listener_secure' => 'https://#DOMAIN#/bitrix/sub/',
        'path_to_modern_listener' => 'http://#DOMAIN#/bitrix/sub/',
        'path_to_modern_listener_secure' => 'https://#DOMAIN#/bitrix/sub/',
        'path_to_mobile_listener' => 'http://#DOMAIN#/bitrix/sub/',
        'path_to_mobile_listener_secure' => 'https://#DOMAIN#/bitrix/sub/',
        'path_to_websocket' => 'ws://#DOMAIN#/bitrix/subws/',
        'path_to_websocket_secure' => 'wss://#DOMAIN#/bitrix/subws/',
        'path_to_publish' => 'http://' . $push['publisher'] . ':9010/bitrix/pub/',
        'path_to_publish_web' => 'http://#DOMAIN#/bitrix/rest/',
        'path_to_publish_web_secure' => 'https://#DOMAIN#/bitrix/rest/',
        'nginx_version' => '4', 'nginx_command_per_hit' => '100', 'nginx' => 'Y',
        'nginx_headers' => 'N', 'push' => 'Y', 'websocket' => 'Y', 'guest' => 'N',
        'signature_key' => $readSecret($push['keyFile']), 'signature_algo' => 'sha256',
        'enable_protobuf' => 'Y', 'limit_max_payload' => 1048576,
        'limit_max_messages_per_request' => 100, 'limit_max_channels_per_request' => 100,
    ];
};
