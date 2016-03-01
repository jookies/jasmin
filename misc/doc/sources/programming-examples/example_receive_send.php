<?php
// Will filter received messages, if the syntax is correct (weather <city name>)
// it will provide a `fake` weather forecast back to the user.
// http://jasminsms.com

$MO_SMS = $_POST;

// Acking back Jasmin is mandatory
echo "ACK/Jasmin";

// Syntax check
if (!preg_match('/^(weather) (.*)/', $MO_SMS['content'], $matches))
    $RESPONSE = "SMS Syntax error, please type 'weather city' to get a fresh weather forecast";
else
    $RESPONSE = $martches[2]." forecast: Sunny 21°C, 13Knots NW light wind";

// Send $RESPONSE back to the user ($MO_SMS['from'])
$baseurl = 'http://127.0.0.1:1401/send'
$params = '?username=foo'
$params.= '&password=bar'
$params.= '&to='.urlencode($MO_SMS['from'])
$params.= '&content='.urlencode($RESPONSE)

$response = file_get_contents($baseurl.$params);

// Note:
// If you need to check if the message is really delivered (or at least, taken by Jasmin for delivery)
// you must test for $response value, it must begin with "Success", c.f. HTTP API doc for more details
