<?php
$baseurl = 'http://127.0.0.1:1401/send'
$params = '?username=fourat'
$params.= '&password=secret'
$params.= '&to='.urlencode('+21698700177')
$params.= '&content='.urlencode('Hello world !')
$response = file_get_contents($baseurl.$params);
?>
