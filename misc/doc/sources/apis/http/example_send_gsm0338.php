<?php
// Sending simple message using PHP
// http://jasminsms.com

$baseurl = 'http://127.0.0.1:1401/send'

$params = '?username=foo'
$params.= '&password=bar'
$params.= '&to='.urlencode('+336222172')
$params.= '&content='.urlencode('Hello world !')

$response = file_get_contents($baseurl.$params);
?>
