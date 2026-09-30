<?php
return [
    'custom' => ['value' => ['marker' => 'base-preserved', 'directory' => __DIR__]],
    'connections' => ['value' => ['analytics' => ['className' => 'CustomConnection']]],
    'cache' => ['value' => ['use_lock' => true]],
    'session' => ['value' => ['regenerateIdAfterLogin' => true, 'handlers' => ['kernel' => 'stale']]],
];
