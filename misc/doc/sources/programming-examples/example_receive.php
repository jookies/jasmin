<?php
// Receiving simple message using PHP through HTTP Post
// This example will store every received SMS to a SQL table
// http://jasminsms.com


$MO_SMS = $_POST;

$db = pg_connect('host=127.0.0.1 port=5432 dbname=sms_demo user=jasmin password=jajapwd');
if (!$db)
    // We'll not ACK the message, Jasmin will resend it later
    die("Error connecting to DB");

$QUERY = "INSERT INTO sms_mo(id, from, to, cid, priority, coding, validity, content) ";
$QUERY.= "VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s');";

$Q = sprintf($QUERY, pg_escape_string($MO_SMS['id']), 
                     pg_escape_string($MO_SMS['from']), 
                     pg_escape_string($MO_SMS['to']), 
                     pg_escape_string($MO_SMS['origin-connector']), 
                     pg_escape_string($MO_SMS['priority']), 
                     pg_escape_string($MO_SMS['coding']), 
                     pg_escape_string($MO_SMS['validity']), 
                     pg_escape_string($MO_SMS['content'])
                     );
pg_query($Q);
pg_close($db);

// Acking back Jasmin is mandatory
echo "ACK/Jasmin";

