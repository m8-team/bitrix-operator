<?php
// Test only: private FastCGI parameter must win over spoofed request headers.
echo defined('SITE_ID') ? SITE_ID : 'undefined';
