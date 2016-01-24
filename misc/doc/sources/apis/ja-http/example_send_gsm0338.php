<?php
// Sending simple message using PHP
// http://jasminsms.com

$baseurl = 'http://127.0.0.1:1401/send'

$params = '?username=fourat'
$params.= '&password=secret'
$params.= '&to='.urlencode('+24206155423')
$params.= '&content='.urlencode('Hello world !')

$response = file_get_contents($baseurl.$params);
?>
