<?php
// E2E only: assert FastCGI transport metadata after Gateway TLS termination.
header('Content-Type: application/json');
echo json_encode([
    'scheme' => $_SERVER['REQUEST_SCHEME'] ?? '',
    'https' => $_SERVER['HTTPS'] ?? '',
    'port' => $_SERVER['SERVER_PORT'] ?? '',
]);
